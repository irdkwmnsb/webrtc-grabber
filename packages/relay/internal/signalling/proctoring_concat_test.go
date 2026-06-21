package signalling

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConcatListEntry: concat list entries must be absolute (ffmpeg resolves
// relative entries against the list file's own directory, which doubled the
// path when storageDirectory was relative) and single quotes must be escaped.
func TestConcatListEntry(t *testing.T) {
	entry := concatListEntry(filepath.Join("records", "proctoring", "s", "seg.webm"))
	inner := strings.TrimSuffix(strings.TrimPrefix(entry, "file '"), "'\n")
	if !filepath.IsAbs(inner) {
		t.Fatalf("entry path must be absolute, got %q", entry)
	}
	if strings.Contains(inner, "records/proctoring/s/records/") {
		t.Fatalf("path doubled: %q", entry)
	}

	q := concatListEntry("/tmp/o'brien/seg.webm")
	if !strings.Contains(q, `'\''`) {
		t.Fatalf("single quote not escaped: %q", q)
	}
}

// TestFinalizeProctoringSession_RelativeStorageDir reproduces the production
// failure: with a RELATIVE storageDirectory (the default "./records") the
// concat list held cwd-relative paths that ffmpeg re-resolved against the list
// file's directory, doubling them. Must now produce a valid full.webm.
func TestFinalizeProctoringSession_RelativeStorageDir(t *testing.T) {
	ffmpegAvailable(t)
	t.Chdir(t.TempDir()) // run with cwd at a temp root; storage stays relative

	storage := "records"
	session := "20260621_181237"
	streamDir := filepath.Join(storage, "proctoring", session, "001", "desktop")
	if err := os.MkdirAll(streamDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	makeRealWebmFile(t, filepath.Join(streamDir, "segment_000000.webm"), 2)
	makeRealWebmFile(t, filepath.Join(streamDir, "segment_000001.webm"), 2)

	finalizeProctoringSession(storage, session)

	full := filepath.Join(streamDir, "full.webm")
	if info, err := os.Stat(full); err != nil || info.Size() == 0 {
		t.Fatalf("full.webm not produced from relative storage dir: err=%v", err)
	}
}
