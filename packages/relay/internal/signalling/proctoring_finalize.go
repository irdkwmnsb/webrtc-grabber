package signalling

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
	peers, err := os.ReadDir(sessionDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("proctoring finalize: cannot read session dir", "dir", sessionDir, "error", err)
		}
		return
	}
	for _, peer := range peers {
		if !peer.IsDir() {
			continue
		}
		peerDir := filepath.Join(sessionDir, peer.Name())
		streams, err := os.ReadDir(peerDir)
		if err != nil {
			continue
		}
		for _, st := range streams {
			if !st.IsDir() {
				continue
			}
			streamDir := filepath.Join(peerDir, st.Name())
			finalizeProctoringStream(streamDir)
		}
	}

	markProctoringFinalized(storageDir, sessionId)
}

// finalizeProctoringStream produces a single playable `full.remuxed.webm` for a
// peer/stream directory. If segment_NNNNNN.webm files are present they are
// concatenated; otherwise the legacy `full.webm` is remuxed as before.
func finalizeProctoringStream(streamDir string) {
	if segments := listSegmentFiles(streamDir); len(segments) > 0 {
		concatSegments(streamDir, segments)
	}
	full := filepath.Join(streamDir, "full.webm")
	if _, err := os.Stat(full); err != nil {
		return
	}
	remuxFile(full)
}

func listSegmentFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "segment_") || !strings.HasSuffix(name, ".webm") {
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// concatSegments uses ffmpeg's concat demuxer to join segments into
// `full.webm`. The original segment files are kept on disk so that even if
// the merged file is corrupt, individual segments remain recoverable.
func concatSegments(dir string, segments []string) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		slog.Warn("ffmpeg not found, skipping proctoring concat", "dir", dir)
		return
	}
	full := filepath.Join(dir, "full.webm")
	if info, err := os.Stat(full); err == nil && info.Size() > 0 {
		// Already concatenated.
		return
	}

	listPath := filepath.Join(dir, "concat_list.txt")
	var buf bytes.Buffer
	for _, s := range segments {
		// concat demuxer with -safe 0 accepts absolute paths.
		fmt.Fprintf(&buf, "file '%s'\n", filepath.Join(dir, s))
	}
	if err := os.WriteFile(listPath, buf.Bytes(), 0o644); err != nil {
		slog.Warn("proctoring concat: cannot write list", "file", listPath, "error", err)
		return
	}
	defer os.Remove(listPath)

	tmp := full + ".tmp"
	ctx, cancel := context.WithTimeout(context.Background(), proctoringFinalizeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-loglevel", "error",
		"-f", "concat", "-safe", "0",
		"-i", listPath,
		"-c", "copy",
		"-f", "webm",
		tmp,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Error("ffmpeg concat failed", "dir", dir, "error", err, "stderr", string(out))
		_ = os.Remove(tmp)
		// Concat with stream copy failed (parameters probably differ
		// between segments). Fall back to re-encoding so the user gets a
		// usable file at the cost of CPU.
		reencodeConcat(dir, listPath, tmp)
		return
	}
	if err := os.Rename(tmp, full); err != nil {
		slog.Error("ffmpeg concat: failed to publish", "output", full, "error", err)
		_ = os.Remove(tmp)
		return
	}
	slog.Info("proctoring segments concatenated", "dir", dir, "segments", len(segments), "output", full)
}

func reencodeConcat(dir, listPath, tmp string) {
	full := filepath.Join(dir, "full.webm")
	ctx, cancel := context.WithTimeout(context.Background(), proctoringFinalizeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-loglevel", "error",
		"-f", "concat", "-safe", "0",
		"-i", listPath,
		"-c:v", "libvpx", "-b:v", "1M", "-crf", "12",
		"-c:a", "libopus",
		"-f", "webm",
		tmp,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Error("ffmpeg reencode concat failed", "dir", dir, "error", err, "stderr", string(out))
		_ = os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, full); err != nil {
		slog.Error("ffmpeg reencode: failed to publish", "output", full, "error", err)
		_ = os.Remove(tmp)
		return
	}
	slog.Info("proctoring segments re-encoded", "dir", dir, "output", full)
}

func remuxFile(input string) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		slog.Warn("ffmpeg not found, skipping proctoring remux", "input", input)
		return
	}

	dir := filepath.Dir(input)
	output := filepath.Join(dir, "full.remuxed.webm")

	if _, err := os.Stat(output); err == nil {
		return
	}

	tmp := output + ".tmp"

	ctx, cancel := context.WithTimeout(context.Background(), proctoringFinalizeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-loglevel", "error",
		"-fflags", "+genpts",
		"-i", input,
		"-c", "copy",
		"-f", "webm",
		tmp,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("ffmpeg remux failed", "input", input, "error", err, "stderr", string(out))
		_ = os.Remove(tmp)
		return
	}

	if err := os.Rename(tmp, output); err != nil {
		slog.Error("ffmpeg remux: failed to publish remuxed file", "output", output, "error", err)
		_ = os.Remove(tmp)
		return
	}

	slog.Info("proctoring file remuxed", "input", input, "output", output)
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
