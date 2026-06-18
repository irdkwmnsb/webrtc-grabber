package signalling

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

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
	CommittedSeq   int64                  `json:"committedSeq"`
	TotalBytes     int64                  `json:"totalBytes"`
	CurrentSegment int                    `json:"currentSegment,omitempty"`
	SegmentBytes   int64                  `json:"segmentBytes,omitempty"`
	Segments       []proctoringSegmentRow `json:"segments,omitempty"`
}

type proctoringSegmentRow struct {
	Index       int   `json:"index"`
	StartSeq    int64 `json:"startSeq"`
	EndSeq      int64 `json:"endSeq"`
	Bytes       int64 `json:"bytes"`
	Chunks      int64 `json:"chunks"`
	FirstChunkMs int64 `json:"firstChunkMs,omitempty"`
	LastChunkMs  int64 `json:"lastChunkMs,omitempty"`
}

const proctoringLegacySegment = -1

func segmentFileName(index int) string {
	if index == proctoringLegacySegment {
		return "full.webm"
	}
	return fmt.Sprintf("segment_%06d.webm", index)
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
	st := proctoringChunkState{CommittedSeq: -1, CurrentSegment: 0}
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
	if st.CommittedSeq == 0 && st.CurrentSegment == 0 && len(st.Segments) == 0 {
		st.CommittedSeq = -1
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

// upsertSegment updates the manifest entry for the given index with the new
// chunk. It assumes that segments are append-only by index — i.e. once we
// move to index N+1 we never go back to N (stragglers from N will have been
// written to segment_N.webm directly via the upload handler).
func upsertSegment(st *proctoringChunkState, index, startSeq, endSeq int, bytes int64, nowMs int64) {
	// Most common case: appending to the last segment.
	if len(st.Segments) > 0 && st.Segments[len(st.Segments)-1].Index == index {
		last := &st.Segments[len(st.Segments)-1]
		last.EndSeq = int64(endSeq)
		last.Bytes += bytes
		last.Chunks++
		last.LastChunkMs = nowMs
		return
	}
	// Otherwise: find or create a row for this index. This handles the case
	// where a straggler chunk arrives for segment N after we already moved to
	// N+1 — we want to update N's row, not start a new one at the tail.
	for i, r := range st.Segments {
		if r.Index == index {
			st.Segments[i].EndSeq = int64(endSeq)
			st.Segments[i].Bytes += bytes
			st.Segments[i].Chunks++
			st.Segments[i].LastChunkMs = nowMs
			return
		}
	}
	st.Segments = append(st.Segments, proctoringSegmentRow{
		Index:        index,
		StartSeq:     int64(startSeq),
		EndSeq:       int64(endSeq),
		Bytes:        bytes,
		Chunks:       1,
		FirstChunkMs: nowMs,
		LastChunkMs:  nowMs,
	})
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
			segmentStr := c.Query("segment")
			advanceStr := c.Query("advanceToSeq")

			if !isSafePathSegment(peerName) || !isSafePathSegment(sessionId) || !isSafePathSegment(seqStr) || !isProctoringStreamKey(streamKey) {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid params")
			}
			if segmentStr != "" && !isSafePathSegment(segmentStr) {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid segment")
			}
			if advanceStr != "" && !isSafePathSegment(advanceStr) {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid advanceToSeq")
			}
			if !s.verifyUploadToken(proctoringScope(sessionId, peerName, streamKey), c.Get(uploadTokenHeader)) {
				return c.Status(fiber.StatusUnauthorized).SendString("Invalid upload token")
			}
			seq, err := strconv.ParseInt(seqStr, 10, 64)
			if err != nil || seq < 0 {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid seq")
			}
			// Missing segment (legacy clients) → 0. Negative is forbidden.
			segment := 0
			if segmentStr != "" {
				segment, err = strconv.Atoi(segmentStr)
				if err != nil || segment < 0 {
					return c.Status(fiber.StatusBadRequest).SendString("Invalid segment")
				}
			}
			// Optional gap-closing marker: lets the client declare that everything
			// between committedSeq+1 and advanceToSeq-1 has been permanently lost
			// (e.g. evicted from a bounded upload queue under network pressure).
			// Server advances committedSeq to advanceToSeq-1 atomically.
			advanceToSeq := int64(-1)
			if advanceStr != "" {
				advanceToSeq, err = strconv.ParseInt(advanceStr, 10, 64)
				if err != nil || advanceToSeq < 0 {
					return c.Status(fiber.StatusBadRequest).SendString("Invalid advanceToSeq")
				}
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

			st, err := loadProctoringState(stateFile)
			if err != nil {
				slog.Error("failed to load proctoring side-state", "stateFile", stateFile, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to load state")
			}

			if seq <= st.CommittedSeq {
				return c.Status(fiber.StatusOK).SendString("OK (already committed)")
			}

			// Apply optional gap-closing marker before the strict in-order
			// check so a client that lost chunks can still make progress.
			if advanceToSeq > st.CommittedSeq+1 {
				slog.Info("proctoring gap closed by client",
					"session", sessionId, "peer", peerName, "stream", streamKey,
					"prevCommitted", st.CommittedSeq, "advancedTo", advanceToSeq-1,
					"gap", advanceToSeq-1-st.CommittedSeq)
				st.CommittedSeq = advanceToSeq - 1
			}

			if seq != st.CommittedSeq+1 {
				return c.Status(fiber.StatusConflict).SendString("Out of order")
			}

			// Forward-only *current segment* marker (used only for manifest /
			// admin display). Actual writes target the file matching the
			// client's segment so a straggler chunk from segment N appending
			// after rotation to N+1 lands in segment_N.webm, not N+1 (their
			// WebM EBML headers are mutually incompatible).
			if segment > st.CurrentSegment {
				st.CurrentSegment = segment
			}
			currentSegmentFile := filepath.Join(dir, segmentFileName(segment))

			// Per-segment recovery: truncate if the on-disk file is larger
			// than what state claims (e.g. server crashed mid-write).
			var segBytes int64
			if len(st.Segments) > 0 && st.Segments[len(st.Segments)-1].Index == segment {
				segBytes = st.Segments[len(st.Segments)-1].Bytes
			}
			if info, err := os.Stat(currentSegmentFile); err == nil && info.Size() > segBytes {
				if err := os.Truncate(currentSegmentFile, segBytes); err != nil {
					slog.Error("failed to truncate proctoring segment", "file", currentSegmentFile, "error", err)
					return c.Status(fiber.StatusInternalServerError).SendString("Failed to truncate")
				}
			}

			out, err := os.OpenFile(currentSegmentFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				slog.Error("failed to open proctoring segment", "file", currentSegmentFile, "error", err)
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
				slog.Error("failed to append proctoring chunk", "file", currentSegmentFile, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to append")
			}
			if err := out.Sync(); err != nil {
				slog.Error("failed to fsync proctoring segment", "file", currentSegmentFile, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to sync")
			}

			nowMs := time.Now().UnixMilli()
			upsertSegment(&st, segment, int(seq), int(seq), n, nowMs)
			st.CommittedSeq = seq
			st.TotalBytes += n
			if err := saveProctoringState(stateFile, st); err != nil {
				slog.Error("failed to save proctoring state", "stateFile", stateFile, "error", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Failed to save state")
			}

			slog.Debug("appended proctoring chunk", "session", sessionId, "peer", peerName, "seq", seq,
				"segment", segment, "sizeKB", n/1024, "total", st.TotalBytes)
			return c.Status(fiber.StatusOK).SendString("OK")
		})
	})
}
