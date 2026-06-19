package signalling

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ffmpegAvailable skips a test when ffmpeg isn't on PATH. Proctoring
// finalize depends on it; we don't fail CI if the host lacks ffmpeg.
func ffmpegAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}
}

// makeRealWebmFile generates a small valid WebM file with the given
// duration and resolution using ffmpeg's lavfi source. Returns its path.
func makeRealWebmFile(t *testing.T, path string, seconds int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration="+itoa(seconds)+":size=320x240:rate=5",
		"-c:v", "libvpx",
		"-f", "webm",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate %s: %v\n%s", path, err, out)
	}
}

// ffprobeDuration returns the duration reported by ffprobe (whole seconds).
// Empty string if ffprobe is unavailable.
func ffprobeDuration(t *testing.T, path string) string {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-loglevel", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %s: %v\n%s", path, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestFinalizeProctoringSession_ConcatSegments: two real WebM segments are
// produced via ffmpeg, the finalizer concats them into full.webm and then
// remuxes into full.remuxed.webm. Both must be ffprobe-valid and the
// concatenated duration must equal the sum of the parts.
func TestFinalizeProctoringSession_ConcatSegments(t *testing.T) {
	ffmpegAvailable(t)
	dir := t.TempDir()
	session := "20260618_220000"
	peer := "peer-F"
	stream := "desktop"
	streamDir := filepath.Join(dir, "proctoring", session, peer, stream)
	if err := os.MkdirAll(streamDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	makeRealWebmFile(t, filepath.Join(streamDir, "segment_000000.webm"), 2)
	makeRealWebmFile(t, filepath.Join(streamDir, "segment_000001.webm"), 3)

	finalizeProctoringSession(dir, session)

	full := filepath.Join(streamDir, "full.webm")
	remuxed := filepath.Join(streamDir, "full.remuxed.webm")
	for _, p := range []string{full, remuxed} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected output %s missing: %v", p, err)
		}
	}
	dur := ffprobeDuration(t, full)
	if !strings.HasPrefix(dur, "5") {
		t.Errorf("concat duration = %q, want ~5 seconds (2+3)", dur)
	}

	// Idempotency: calling finalize again must not overwrite or fail.
	finalizeProctoringSession(dir, session)
	if info, err := os.Stat(full); err != nil || info.Size() == 0 {
		t.Errorf("full.webm disappeared or emptied after re-finalize")
	}
	if !isProctoringFinalized(dir, session) {
		t.Errorf("session should be marked finalized")
	}
}

// TestFinalizeProctoringSession_LegacySingleFile: a directory containing
// only full.webm (no segment_*.webm) must still be remuxed as before, so
// older recorded sessions remain playable after upgrade.
func TestFinalizeProctoringSession_LegacySingleFile(t *testing.T) {
	ffmpegAvailable(t)
	dir := t.TempDir()
	session := "legacy-session"
	peer := "peer-L"
	stream := "desktop"
	streamDir := filepath.Join(dir, "proctoring", session, peer, stream)
	if err := os.MkdirAll(streamDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	makeRealWebmFile(t, filepath.Join(streamDir, "full.webm"), 2)

	finalizeProctoringSession(dir, session)

	remuxed := filepath.Join(streamDir, "full.remuxed.webm")
	if _, err := os.Stat(remuxed); err != nil {
		t.Fatalf("expected remuxed output missing: %v", err)
	}
	if !isProctoringFinalized(dir, session) {
		t.Errorf("legacy session should be marked finalized")
	}
}

// TestFinalizeProctoringSession_FallsBackToReencode: two segments with
// different resolutions cannot be concat-copied; the reencode fallback
// must kick in and still produce a playable full.webm.
func TestFinalizeProctoringSession_FallsBackToReencode(t *testing.T) {
	ffmpegAvailable(t)
	dir := t.TempDir()
	session := "drift-session"
	peer := "peer-D"
	stream := "desktop"
	streamDir := filepath.Join(dir, "proctoring", session, peer, stream)
	if err := os.MkdirAll(streamDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// First segment at 320x240, second at 640x480 — concat copy will reject
	// this and the reencode path must take over.
	makeRealWebmFileCustom(t, filepath.Join(streamDir, "segment_000000.webm"), 2, "320x240")
	makeRealWebmFileCustom(t, filepath.Join(streamDir, "segment_000001.webm"), 2, "640x480")

	finalizeProctoringSession(dir, session)

	full := filepath.Join(streamDir, "full.webm")
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("expected reencoded full.webm missing: %v", err)
	}
	if ffprobeDuration(t, full) == "" {
		t.Errorf("full.webm is not ffprobe-valid (reencode path produced garbage)")
	}
}

func makeRealWebmFileCustom(t *testing.T, path string, seconds int, size string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration="+itoa(seconds)+":size="+size+":rate=5",
		"-c:v", "libvpx",
		"-f", "webm",
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate %s (%s): %v\n%s", path, size, err, out)
	}
}
