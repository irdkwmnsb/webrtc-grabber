package signalling

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const (
	testPeer    = "peer-A"
	testSession = "20260618_120000"
	testStream  = "desktop"
)

func uploadToken(t *testing.T, e *testEnv) string {
	t.Helper()
	return e.signToken(proctoringScope(testSession, testPeer, testStream))
}

// assertState loads state.json for a stream and reports the parsed value,
// failing the test if the file is missing or unparseable.
func assertState(t *testing.T, e *testEnv, session, peer, stream string) proctoringChunkState {
	t.Helper()
	stateFile := filepath.Join(e.proctoringStreamDir(session, peer, stream), "state.json")
	st, err := loadProctoringState(stateFile)
	if err != nil {
		t.Fatalf("loadProctoringState: %v", err)
	}
	return st
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("file %s content = %q, want %q", path, string(got), want)
	}
}

// TestProctoringUpload_HappyPath: three chunks land in order on segment 0
// and produce a single segment_NNNNNN.webm whose bytes concatenate the
// payloads. state.json tracks the running totals.
func TestProctoringUpload_HappyPath(t *testing.T) {
	e := newTestEnv(t)
	tok := uploadToken(t, e)

	var assembled string
	for seq, payload := range []string{"aaa", "bbb", "ccc"} {
		code, body := e.proctoringUpload(testPeer, testSession, testStream, seq, 0, []byte(payload), tok, -1)
		if code != 200 || body != "OK" {
			t.Fatalf("seq=%d: got (%d, %q), want (200, OK)", seq, code, body)
		}
		assembled += payload
	}

	dir := e.proctoringStreamDir(testSession, testPeer, testStream)
	assertFileContains(t, filepath.Join(dir, "segment_000000.webm"), assembled)

	st := assertState(t, e, testSession, testPeer, testStream)
	if st.CommittedSeq != 2 {
		t.Errorf("CommittedSeq = %d, want 2", st.CommittedSeq)
	}
	if st.TotalBytes != int64(len(assembled)) {
		t.Errorf("TotalBytes = %d, want %d", st.TotalBytes, len(assembled))
	}
	if st.CurrentSegment != 0 {
		t.Errorf("CurrentSegment = %d, want 0", st.CurrentSegment)
	}
	if len(st.Segments) != 1 {
		t.Fatalf("Segments len = %d, want 1", len(st.Segments))
	}
	if st.Segments[0].Chunks != 3 {
		t.Errorf("segment 0 Chunks = %d, want 3", st.Segments[0].Chunks)
	}
}

// TestProctoringUpload_OutOfOrderRejected: a future seq is rejected with
// 409 until the missing predecessor arrives.
func TestProctoringUpload_OutOfOrderRejected(t *testing.T) {
	e := newTestEnv(t)
	tok := uploadToken(t, e)

	if code, _ := e.proctoringUpload(testPeer, testSession, testStream, 0, 0, []byte("a"), tok, -1); code != 200 {
		t.Fatalf("seq=0: got %d, want 200", code)
	}
	code, _ := e.proctoringUpload(testPeer, testSession, testStream, 5, 0, []byte("skip"), tok, -1)
	if code != 409 {
		t.Fatalf("seq=5 (gap): got %d, want 409", code)
	}
}

// TestProctoringUpload_AlreadyCommittedIsIdempotent: re-uploading a chunk
// the server already has returns 200, not 409, so retries are safe.
func TestProctoringUpload_AlreadyCommittedIsIdempotent(t *testing.T) {
	e := newTestEnv(t)
	tok := uploadToken(t, e)

	for _, seq := range []int{0, 1, 2} {
		if code, _ := e.proctoringUpload(testPeer, testSession, testStream, seq, 0, []byte("x"), tok, -1); code != 200 {
			t.Fatalf("initial seq=%d: got %d", seq, code)
		}
	}
	// Replay seq=1 with different content — server must not overwrite.
	if code, body := e.proctoringUpload(testPeer, testSession, testStream, 1, 0, []byte("REPLAY"), tok, -1); code != 200 || body == "" {
		t.Fatalf("replay seq=1: got (%d, %q)", code, body)
	} else if body != "OK (already committed)" {
		t.Errorf("replay seq=1 body = %q, want \"OK (already committed)\"", body)
	}
	dir := e.proctoringStreamDir(testSession, testPeer, testStream)
	assertFileContains(t, filepath.Join(dir, "segment_000000.webm"), "xxx")
}

// TestProctoringUpload_AdvanceToSeqClosesGap: client declares that
// everything between committedSeq+1 and advanceToSeq-1 was lost; server
// advances committedSeq atomically with the upload.
func TestProctoringUpload_AdvanceToSeqClosesGap(t *testing.T) {
	e := newTestEnv(t)
	tok := uploadToken(t, e)

	for _, seq := range []int{0, 1} {
		if code, _ := e.proctoringUpload(testPeer, testSession, testStream, seq, 0, []byte("x"), tok, -1); code != 200 {
			t.Fatalf("seq=%d: got %d", seq, code)
		}
	}
	// Gap: try seq=10 without advance → 409.
	if code, _ := e.proctoringUpload(testPeer, testSession, testStream, 10, 1, []byte("y"), tok, -1); code != 409 {
		t.Fatalf("gap seq=10 (no advance): got %d, want 409", code)
	}
	// Same request with advanceToSeq=10 → 200, gap closed.
	if code, body := e.proctoringUpload(testPeer, testSession, testStream, 10, 1, []byte("y"), tok, 10); code != 200 || body != "OK" {
		t.Fatalf("gap seq=10 (with advance): got (%d, %q), want (200, OK)", code, body)
	}
	// Subsequent seq=11 must succeed without advance.
	if code, _ := e.proctoringUpload(testPeer, testSession, testStream, 11, 1, []byte("z"), tok, -1); code != 200 {
		t.Fatalf("seq=11 after gap: got %d, want 200", code)
	}

	st := assertState(t, e, testSession, testPeer, testStream)
	if st.CommittedSeq != 11 {
		t.Errorf("CommittedSeq = %d, want 11", st.CommittedSeq)
	}
	if len(st.Segments) != 2 {
		t.Errorf("Segments len = %d, want 2 (seg 0 with chunks 0-1, seg 1 with chunks 10-11)", len(st.Segments))
	}
}

// TestProctoringUpload_AdvanceRejectsBackward: advanceToSeq less than the
// current committedSeq+1 is a no-op (must not rewind state).
func TestProctoringUpload_AdvanceRejectsBackward(t *testing.T) {
	e := newTestEnv(t)
	tok := uploadToken(t, e)

	for _, seq := range []int{0, 1, 2, 3, 4} {
		if code, _ := e.proctoringUpload(testPeer, testSession, testStream, seq, 0, []byte("x"), tok, -1); code != 200 {
			t.Fatalf("seq=%d: got %d", seq, code)
		}
	}
	// seq=6 with advanceToSeq=3 — must not rewind committedSeq below 5.
	// After applying advance (no-op since 3 < 5+1=6), seq=6 should still
	// be 409 because committedSeq=4 (after 5 chunks 0..4) and we expect 5 next.
	if code, _ := e.proctoringUpload(testPeer, testSession, testStream, 6, 0, []byte("y"), tok, 3); code != 409 {
		t.Fatalf("seq=6 with advanceToSeq=3: got %d, want 409 (advance must not rewind)", code)
	}
	st := assertState(t, e, testSession, testPeer, testStream)
	if st.CommittedSeq != 4 {
		t.Errorf("CommittedSeq = %d, want 4 (must not have been rewound)", st.CommittedSeq)
	}
}

// TestProctoringUpload_SegmentRotation: client rotates to segment 1 mid
// session; both segment files exist with the right payload boundaries and
// state.json records both rows.
func TestProctoringUpload_SegmentRotation(t *testing.T) {
	e := newTestEnv(t)
	tok := uploadToken(t, e)

	// Seg 0: chunks 0,1,2.
	for seq, p := range []string{"a", "b", "c"} {
		e.proctoringUpload(testPeer, testSession, testStream, seq, 0, []byte(p), tok, -1)
	}
	// Seg 1: chunks 3,4.
	e.proctoringUpload(testPeer, testSession, testStream, 3, 1, []byte("d"), tok, -1)
	e.proctoringUpload(testPeer, testSession, testStream, 4, 1, []byte("e"), tok, -1)

	dir := e.proctoringStreamDir(testSession, testPeer, testStream)
	assertFileContains(t, filepath.Join(dir, "segment_000000.webm"), "abc")
	assertFileContains(t, filepath.Join(dir, "segment_000001.webm"), "de")

	st := assertState(t, e, testSession, testPeer, testStream)
	if st.CommittedSeq != 4 || st.CurrentSegment != 1 {
		t.Errorf("state = committed=%d seg=%d, want 4/1", st.CommittedSeq, st.CurrentSegment)
	}
	if len(st.Segments) != 2 {
		t.Fatalf("Segments len = %d, want 2", len(st.Segments))
	}
	if st.Segments[0].StartSeq != 0 || st.Segments[0].EndSeq != 2 {
		t.Errorf("seg 0 range = [%d,%d], want [0,2]", st.Segments[0].StartSeq, st.Segments[0].EndSeq)
	}
	if st.Segments[1].StartSeq != 3 || st.Segments[1].EndSeq != 4 {
		t.Errorf("seg 1 range = [%d,%d], want [3,4]", st.Segments[1].StartSeq, st.Segments[1].EndSeq)
	}
}

// TestProctoringUpload_StragglerFromPreviousSegment: a late retry for a
// chunk on segment 0 lands after rotation to segment 1. It must NOT be
// appended to segment_000001.webm (different WebM EBML header), and not
// corrupt state.
func TestProctoringUpload_StragglerFromPreviousSegment(t *testing.T) {
	e := newTestEnv(t)
	tok := uploadToken(t, e)

	// Already-committed straggler: server returns OK (already committed)
	// and does NOT touch any file (segment 0 already has the bytes).
	e.proctoringUpload(testPeer, testSession, testStream, 0, 0, []byte("orig"), tok, -1)
	e.proctoringUpload(testPeer, testSession, testStream, 1, 1, []byte("newseg"), tok, -1)

	// Late "retry" of seq=0 — already committed, idempotent.
	code, body := e.proctoringUpload(testPeer, testSession, testStream, 0, 0, []byte("LATE"), tok, -1)
	if code != 200 || body != "OK (already committed)" {
		t.Fatalf("straggler seq=0: got (%d, %q), want (200, OK (already committed))", code, body)
	}
	dir := e.proctoringStreamDir(testSession, testPeer, testStream)
	assertFileContains(t, filepath.Join(dir, "segment_000000.webm"), "orig")
	assertFileContains(t, filepath.Join(dir, "segment_000001.webm"), "newseg")
}

// TestProctoringUpload_RejectsMissingToken / wrong token / wrong scope.
func TestProctoringUpload_TokenFailures(t *testing.T) {
	e := newTestEnv(t)
	type tc struct {
		name  string
		token string
	}
	cases := []tc{
		{"missing", ""},
		{"garbage", "not-a-real-token"},
		{"wrong-secret", e2eSign("foreign-secret", proctoringScope(testSession, testPeer, testStream))},
		{"wrong-scope", e.signToken(proctoringScope(testSession, testPeer, "webcam"))},
		{"wrong-peer", e.signToken(proctoringScope(testSession, "peer-B", testStream))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _ := e.proctoringUpload(testPeer, testSession, testStream, 0, 0, []byte("x"), c.token, -1)
			if code != 401 {
				t.Fatalf("token %q: got %d, want 401", c.name, code)
			}
		})
	}
}

// TestProctoringUpload_RejectsInvalidParams: bad path components and bad
// query params return 400 instead of touching the filesystem.
func TestProctoringUpload_RejectsInvalidParams(t *testing.T) {
	e := newTestEnv(t)
	tok := uploadToken(t, e)

	cases := []struct {
		name string
		peer string
		sess string
		strm string
		seq  string
	}{
		{"bad-peer", "..", testSession, testStream, "0"},
		{"bad-session", testPeer, "..", testStream, "0"},
		{"bad-stream", testPeer, testSession, "weird", "0"},
		{"bad-seq", testPeer, testSession, testStream, "not-a-number"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := "/api/agent/" + c.peer + "/proctoring_upload?sessionId=" + c.sess + "&streamKey=" + c.strm + "&seq=" + c.seq
			req := newMultipartUpload(e, path, []byte("x"), tok)
			resp, err := e.app.Test(req, -1)
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Fatalf("got %d, want 400", resp.StatusCode)
			}
		})
	}
}

// TestProctoringStateEndpoint: read-only state endpoint returns what the
// uploader wrote, useful for the client reconcile flow.
func TestProctoringStateEndpoint(t *testing.T) {
	e := newTestEnv(t)
	tok := uploadToken(t, e)
	e.proctoringUpload(testPeer, testSession, testStream, 0, 0, []byte("x"), tok, -1)
	e.proctoringUpload(testPeer, testSession, testStream, 1, 0, []byte("y"), tok, -1)

	url := "/api/agent/" + testPeer + "/proctoring_state?sessionId=" + testSession + "&streamKey=" + testStream
	req := newGet(e, url, tok)
	resp, err := e.app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	var st proctoringChunkState
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.CommittedSeq != 1 {
		t.Errorf("CommittedSeq = %d, want 1", st.CommittedSeq)
	}
}
