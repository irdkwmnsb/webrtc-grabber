package signalling

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	proctoringFinalizeGrace   = 60 * time.Second
	proctoringFinalizeTimeout = 10 * time.Minute
)

func finalizeProctoringSession(storageDir, sessionId string) {
	if sessionId == "" {
		return
	}
	sessionDir := filepath.Join(storageDir, "proctoring", sessionId)
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
