package signalling

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

	// Misaligned segments (legacy recordings where a stopped recorder's
	// headerless tail was tagged into the next segment) are not standalone
	// WebM files. The concat demuxer doesn't reliably error on them — it can
	// silently emit a truncated file — so detect the condition up front by
	// checking that every segment begins with a real EBML header, and go
	// straight to byte-level reconstruction otherwise.
	if !segmentsAreClean(dir, segments) {
		slog.Warn("proctoring concat: segments not header-aligned, reconstructing", "dir", dir)
		if reconstructFromHeaders(dir, segments, full) {
			return
		}
		slog.Error("proctoring concat: reconstruction failed for misaligned segments", "dir", dir)
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
		slog.Warn("ffmpeg concat (copy) failed, trying header-split reconstruction",
			"dir", dir, "error", err, "stderr", string(out))
		_ = os.Remove(tmp)
		// The straightforward concat demuxer fails when segment files are not
		// standalone-decodable — e.g. legacy recordings where a stopped
		// recorder's headerless tail was tagged into the next segment, leaving
		// that segment's EBML header buried mid-file. Rebuild from the raw byte
		// stream by re-splitting at genuine WebM headers before paying for a
		// full re-encode.
		if reconstructFromHeaders(dir, segments, full) {
			return
		}
		// Still no luck (parameters probably differ between runs). Fall back to
		// re-encoding so the user gets a usable file at the cost of CPU.
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

// reconstructFromHeaders rebuilds full.webm from segment files whose
// boundaries are misaligned — each segment may begin mid-stream because a
// stopped recorder's flushed tail was tagged into the next segment (the bug
// fixed in capture.html). It concatenates every segment's bytes back into the
// original continuous stream, re-splits that stream at genuine WebM EBML
// headers into independent recorder runs, remuxes each run to normalise
// timestamps, then joins the runs with the concat demuxer. Returns true if it
// published a usable full.webm. Works for current (clean) recordings too — each
// clean segment is simply its own run.
func reconstructFromHeaders(dir string, segments []string, full string) bool {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return false
	}

	work, err := os.MkdirTemp(dir, "reconstruct-")
	if err != nil {
		slog.Warn("proctoring reconstruct: cannot create workdir", "dir", dir, "error", err)
		return false
	}
	defer os.RemoveAll(work)

	raw := filepath.Join(work, "full_raw.webm")
	if !concatRawBytes(dir, segments, raw) {
		return false
	}

	offsets, err := ebmlHeaderOffsets(raw)
	if err != nil {
		slog.Warn("proctoring reconstruct: header scan failed", "file", raw, "error", err)
		return false
	}
	if len(offsets) == 0 {
		slog.Warn("proctoring reconstruct: no WebM headers found", "file", raw)
		return false
	}

	var listBuf bytes.Buffer
	runs := 0
	for i, start := range offsets {
		end := int64(-1) // -1 means "to EOF"
		if i+1 < len(offsets) {
			end = offsets[i+1]
		}
		runPath := filepath.Join(work, fmt.Sprintf("run_%03d.webm", i))
		if err := extractRange(raw, start, end, runPath); err != nil {
			slog.Warn("proctoring reconstruct: extract failed", "run", i, "error", err)
			continue
		}
		fixedPath := filepath.Join(work, fmt.Sprintf("fix_%03d.webm", i))
		if !remuxToFile(runPath, fixedPath) {
			continue
		}
		fmt.Fprintf(&listBuf, "file '%s'\n", fixedPath)
		runs++
	}
	if runs == 0 {
		return false
	}

	listPath := filepath.Join(work, "runs.txt")
	if err := os.WriteFile(listPath, listBuf.Bytes(), 0o644); err != nil {
		slog.Warn("proctoring reconstruct: cannot write list", "file", listPath, "error", err)
		return false
	}

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
		slog.Error("proctoring reconstruct: final concat failed", "dir", dir, "error", err, "stderr", string(out))
		_ = os.Remove(tmp)
		return false
	}
	if err := os.Rename(tmp, full); err != nil {
		slog.Error("proctoring reconstruct: cannot publish", "output", full, "error", err)
		_ = os.Remove(tmp)
		return false
	}
	slog.Info("proctoring reconstructed via header split", "dir", dir, "runs", runs, "output", full)
	return true
}

// segmentsAreClean reports whether every segment file begins with a genuine
// WebM EBML header, i.e. each is a standalone recorder run that the concat
// demuxer can join directly. A false result means at least one segment starts
// mid-stream and the bytes must be reconstructed instead.
func segmentsAreClean(dir string, segments []string) bool {
	for _, s := range segments {
		if !startsWithEBMLHeader(filepath.Join(dir, s)) {
			return false
		}
	}
	return true
}

func startsWithEBMLHeader(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	// Enough to cover the magic, a multi-byte size vint and the first child ID.
	head := make([]byte, 16)
	n, _ := io.ReadFull(f, head)
	return validEBMLHeader(head[:n], 0)
}

// concatRawBytes writes every segment's bytes, in order, to dst — yielding the
// original continuous recorder byte stream regardless of where the file
// boundaries fell.
func concatRawBytes(dir string, segments []string, dst string) bool {
	out, err := os.Create(dst)
	if err != nil {
		slog.Warn("proctoring reconstruct: cannot create raw file", "file", dst, "error", err)
		return false
	}
	defer out.Close()
	for _, s := range segments {
		in, err := os.Open(filepath.Join(dir, s))
		if err != nil {
			slog.Warn("proctoring reconstruct: cannot open segment", "segment", s, "error", err)
			return false
		}
		_, cerr := io.Copy(out, in)
		_ = in.Close()
		if cerr != nil {
			slog.Warn("proctoring reconstruct: copy failed", "segment", s, "error", cerr)
			return false
		}
	}
	return true
}

// extractRange copies srcPath[start:end) into dst. end < 0 copies to EOF.
func extractRange(srcPath string, start, end int64, dst string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := in.Seek(start, io.SeekStart); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	var r io.Reader = in
	if end >= 0 {
		r = io.LimitReader(in, end-start)
	}
	_, err = io.Copy(out, r)
	return err
}

// remuxToFile remuxes a single recorder run, regenerating timestamps so the
// downstream concat demuxer can offset each run onto a continuous timeline.
func remuxToFile(input, output string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), proctoringFinalizeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y", "-loglevel", "error",
		"-fflags", "+genpts",
		"-i", input,
		"-c", "copy",
		"-f", "webm",
		output,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("proctoring reconstruct: run remux failed", "input", input, "error", err, "stderr", string(out))
		return false
	}
	return true
}

// ebmlHeaderOffsets returns the byte offsets of every genuine WebM EBML header
// in the file at path.
func ebmlHeaderOffsets(path string) ([]int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ebmlHeaderOffsetsBytes(data), nil
}

// ebmlHeaderOffsetsBytes scans data for EBML header start markers (0x1A45DFA3),
// keeping only occurrences that validate as real headers (see validEBMLHeader)
// so byte sequences that happen to appear inside cluster payload are ignored.
func ebmlHeaderOffsetsBytes(data []byte) []int64 {
	magic := []byte{0x1A, 0x45, 0xDF, 0xA3}
	var offs []int64
	for i := 0; ; {
		j := bytes.Index(data[i:], magic)
		if j < 0 {
			break
		}
		p := i + j
		if validEBMLHeader(data, p) {
			offs = append(offs, int64(p))
		}
		i = p + 1
	}
	return offs
}

// validEBMLHeader reports whether the 0x1A45DFA3 at p begins a real EBML
// header. A genuine header is the magic, a vint-encoded size, then child
// elements starting with EBMLVersion (ID 0x4286) — the layout MediaRecorder
// emits. This rejects coincidental magic-like bytes inside VP8/VP9 payload.
func validEBMLHeader(data []byte, p int) bool {
	sizePos := p + 4
	if sizePos >= len(data) {
		return false
	}
	n := vintLen(data[sizePos])
	if n == 0 {
		return false
	}
	child := sizePos + n
	if child+1 >= len(data) {
		return false
	}
	return data[child] == 0x42 && data[child+1] == 0x86
}

// vintLen returns the length in bytes of an EBML variable-length integer given
// its first byte (the position of the highest set bit), or 0 if invalid.
func vintLen(b byte) int {
	for i := range 8 {
		if b&(0x80>>i) != 0 {
			return i + 1
		}
	}
	return 0
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
