package signalling

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
)

const (
	proctoringChunkMaxBytes = 32 * 1024 * 1024
	recordUploadMaxBytes    = 50 * 1024 * 1024
)

func isSafePathSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, `/\`)
}

func limitBody(max int) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if c.Request().Header.ContentLength() > max {
			return c.Status(fiber.StatusRequestEntityTooLarge).SendString("Body too large")
		}
		return c.Next()
	}
}

type proctoringChunkState struct {
	CommittedSeq int64 `json:"committedSeq"`
	TotalBytes   int64 `json:"totalBytes"`
}

var proctoringLocks sync.Map

func proctoringLockKey(sessionId, peerName, streamKey string) string {
	return sessionId + "/" + peerName + "/" + streamKey
}

func isProctoringStreamKey(s string) bool {
	return slices.Contains(proctoringStreamKeys(), s)
}

func proctoringLock(key string) *sync.Mutex {
	v, _ := proctoringLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func cleanupProctoringLocks(sessionId string) {
	prefix := sessionId + "/"
	proctoringLocks.Range(func(k, _ any) bool {
		if key, ok := k.(string); ok && strings.HasPrefix(key, prefix) {
			proctoringLocks.Delete(k)
		}
		return true
	})
}

func loadProctoringState(stateFile string) (proctoringChunkState, error) {
	st := proctoringChunkState{CommittedSeq: -1}
	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, err
	}
	return st, nil
}

func saveProctoringState(stateFile string, st proctoringChunkState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := stateFile + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, stateFile)
}

func (s *Server) setupAgentApi() {
	s.app.Route("/api/agent", func(router fiber.Router) {
		router.Post("/:peerName/record_upload", limitBody(recordUploadMaxBytes), func(c *fiber.Ctx) error {
			if s.config.Record.StorageDir == "" {
				return c.Status(fiber.StatusMethodNotAllowed).SendString("Record storage is not enabled")
			}

			peerName := c.Params("peerName")
			if !isSafePathSegment(peerName) {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid peerName")
			}
			if !s.verifyUploadToken(recordScope(peerName), c.Get(uploadTokenHeader)) {
				return c.Status(fiber.StatusUnauthorized).SendString("Invalid upload token")
			}

			file, err := c.FormFile("file")
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("No file to upload")
			}
			safeName := filepath.Base(file.Filename)
			if !isSafePathSegment(safeName) || !strings.HasSuffix(safeName, ".webm") {
				return c.Status(fiber.StatusBadRequest).SendString("File has incorrect name or extension")
			}

			slog.Info("store agent record file", "filename", safeName, "sizeKB", file.Size/1024)

			destination := filepath.Join(s.config.Record.StorageDir, peerName+"_"+safeName)
			if err := c.SaveFile(file, destination); err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Failed to upload file")
			}

			return c.Status(fiber.StatusOK).SendString("OK")
		})

		router.Get("/:peerName/proctoring_state", func(c *fiber.Ctx) error {
			if s.config.Record.StorageDir == "" {
				return c.Status(fiber.StatusMethodNotAllowed).SendString("Record storage is not enabled")
			}
			peerName := c.Params("peerName")
			sessionId := c.Query("sessionId")
			streamKey := c.Query("streamKey")
			if !isSafePathSegment(peerName) || !isSafePathSegment(sessionId) || !isProctoringStreamKey(streamKey) {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid params")
			}
			if !s.verifyUploadToken(proctoringScope(sessionId, peerName, streamKey), c.Get(uploadTokenHeader)) {
				return c.Status(fiber.StatusUnauthorized).SendString("Invalid upload token")
			}
			stateFile := filepath.Join(s.config.Record.StorageDir, "proctoring", sessionId, peerName, streamKey, "state.json")
			st, err := loadProctoringState(stateFile)
			if err != nil {
				slog.Error("failed to load proctoring side-state", "stateFile", stateFile, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to load state")
			}
			return c.JSON(st)
		})

		router.Post("/:peerName/proctoring_upload", limitBody(proctoringChunkMaxBytes), func(c *fiber.Ctx) error {
			if s.config.Record.StorageDir == "" {
				return c.Status(fiber.StatusMethodNotAllowed).SendString("Record storage is not enabled")
			}

			peerName := c.Params("peerName")
			sessionId := c.Query("sessionId")
			streamKey := c.Query("streamKey")
			seqStr := c.Query("seq")

			if !isSafePathSegment(peerName) || !isSafePathSegment(sessionId) || !isSafePathSegment(seqStr) || !isProctoringStreamKey(streamKey) {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid params")
			}
			if !s.verifyUploadToken(proctoringScope(sessionId, peerName, streamKey), c.Get(uploadTokenHeader)) {
				return c.Status(fiber.StatusUnauthorized).SendString("Invalid upload token")
			}
			seq, err := strconv.ParseInt(seqStr, 10, 64)
			if err != nil || seq < 0 {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid seq")
			}

			file, err := c.FormFile("file")
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("No file to upload")
			}

			dir := filepath.Join(s.config.Record.StorageDir, "proctoring", sessionId, peerName, streamKey)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				slog.Error("failed to create proctoring dir", "dir", dir, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to create dir")
			}

			lock := proctoringLock(proctoringLockKey(sessionId, peerName, streamKey))
			lock.Lock()
			defer lock.Unlock()

			stateFile := filepath.Join(dir, "state.json")
			fullFile := filepath.Join(dir, "full.webm")

			st, err := loadProctoringState(stateFile)
			if err != nil {
				slog.Error("failed to load proctoring side-state", "stateFile", stateFile, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to load state")
			}

			if seq <= st.CommittedSeq {
				return c.Status(fiber.StatusOK).SendString("OK (already committed)")
			}
			if seq != st.CommittedSeq+1 {
				return c.Status(fiber.StatusConflict).SendString("Out of order")
			}

			if info, err := os.Stat(fullFile); err == nil && info.Size() > st.TotalBytes {
				if err := os.Truncate(fullFile, st.TotalBytes); err != nil {
					slog.Error("failed to truncate proctoring file", "file", fullFile, "error", err)
					return c.Status(fiber.StatusInternalServerError).SendString("Failed to truncate")
				}
			}

			out, err := os.OpenFile(fullFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				slog.Error("failed to open proctoring file", "file", fullFile, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to open")
			}
			defer out.Close()

			src, err := file.Open()
			if err != nil {
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to read upload")
			}
			defer src.Close()

			n, err := io.Copy(out, src)
			if err != nil {
				slog.Error("failed to append proctoring chunk", "file", fullFile, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to append")
			}
			if err := out.Sync(); err != nil {
				slog.Error("failed to fsync proctoring file", "file", fullFile, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to sync")
			}

			next := proctoringChunkState{CommittedSeq: seq, TotalBytes: st.TotalBytes + n}
			if err := saveProctoringState(stateFile, next); err != nil {
				slog.Error("failed to save proctoring state", "stateFile", stateFile, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to save state")
			}

			slog.Debug("appended proctoring chunk", "session", sessionId, "peer", peerName, "seq", seq, "sizeKB", n/1024, "total", next.TotalBytes)
			return c.Status(fiber.StatusOK).SendString("OK")
		})
	})
}
