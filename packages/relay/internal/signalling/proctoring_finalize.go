package signalling

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	proctoringFinalizeGrace   = 60 * time.Second
	proctoringFinalizeTimeout = 10 * time.Minute
	proctoringFinalizedMarker = "finalized"
)

var finalizeLocks sync.Map

func finalizeLock(sessionId string) *sync.Mutex {
	v, _ := finalizeLocks.LoadOrStore(sessionId, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func proctoringSessionDir(storageDir, sessionId string) string {
	return filepath.Join(storageDir, "proctoring", sessionId)
}

func isProctoringFinalized(storageDir, sessionId string) bool {
	_, err := os.Stat(filepath.Join(proctoringSessionDir(storageDir, sessionId), proctoringFinalizedMarker))
	return err == nil
}

func markProctoringFinalized(storageDir, sessionId string) {
	marker := filepath.Join(proctoringSessionDir(storageDir, sessionId), proctoringFinalizedMarker)
	f, err := os.Create(marker)
	if err != nil {
		slog.Warn("proctoring finalize: failed to write marker", "marker", marker, "error", err)
		return
	}
	_ = f.Close()
}

func finalizeProctoringSession(storageDir, sessionId string) {
	if sessionId == "" {
		return
	}

	lock := finalizeLock(sessionId)
	lock.Lock()
	defer func() {
		finalizeLocks.Delete(sessionId)
		cleanupProctoringLocks(sessionId)
		lock.Unlock()
	}()

	if isProctoringFinalized(storageDir, sessionId) {
		return
	}

	sessionDir := proctoringSessionDir(storageDir, sessionId)
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("proctoring finalize: cannot read session dir", "dir", sessionDir, "error", err)
		}
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		full := filepath.Join(sessionDir, e.Name(), "full.webm")
		if _, err := os.Stat(full); err != nil {
			continue
		}
		remuxFile(full)
	}

	markProctoringFinalized(storageDir, sessionId)
}

func remuxFile(input string) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		slog.Warn("ffmpeg not found, skipping proctoring remux", "input", input)
		return
	}

	dir := filepath.Dir(input)
	tmp := filepath.Join(dir, "full.fixed.webm")

	ctx, cancel := context.WithTimeout(context.Background(), proctoringFinalizeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-loglevel", "error", "-i", input, "-c", "copy", tmp)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("ffmpeg remux failed", "input", input, "error", err, "stderr", string(out))
		_ = os.Remove(tmp)
		return
	}

	if err := os.Rename(tmp, input); err != nil {
		slog.Error("ffmpeg remux: failed to swap file", "input", input, "error", err)
		_ = os.Remove(tmp)
		return
	}

	slog.Info("proctoring file remuxed", "file", input)
}

func (s *Server) scheduleProctoringFinalize(sessionId string) {
	if sessionId == "" || s.config.Record.StorageDir == "" {
		return
	}
	storageDir := s.config.Record.StorageDir
	go func() {
		time.Sleep(proctoringFinalizeGrace)
		finalizeProctoringSession(storageDir, sessionId)
	}()
}

func sweepProctoringSessions(storageDir, activeSessionId string) {
	if storageDir == "" {
		return
	}
	base := filepath.Join(storageDir, "proctoring")
	entries, err := os.ReadDir(base)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("proctoring sweep: cannot read", "dir", base, "error", err)
		}
		return
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == activeSessionId {
			continue
		}
		if isProctoringFinalized(storageDir, e.Name()) {
			continue
		}
		sid := e.Name()
		slog.Info("proctoring sweep: finalizing leftover session", "sessionId", sid)
		go finalizeProctoringSession(storageDir, sid)
	}
}
