package signalling

import (
	"encoding/json"
	"testing"
	"time"
)

// TestProctoringLifecycle_FullFlow drives the proctoring state machine
// through start → pause → resume → stop via the admin API and verifies
// the response body at each step.
func TestProctoringLifecycle_FullFlow(t *testing.T) {
	e := newTestEnv(t)

	status, startBody := e.adminPost("/api/admin/proctoring/start", `{"chunkDurationMs":1000,"fps":5,"videoBitrate":100000}`)
	if status != 200 {
		t.Fatalf("start: got %d, want 200 (body: %s)", status, startBody)
	}
	st := decodeProctoringState(t, startBody)
	if !st.IsActive() {
		t.Errorf("after start: state = %+v, want active", st)
	}
	if st.SessionId == "" {
		t.Errorf("empty sessionId after start")
	}
	sessionId := st.SessionId

	// Second start must conflict.
	if code, _ := e.adminPost("/api/admin/proctoring/start", `{"chunkDurationMs":1000,"fps":5,"videoBitrate":100000}`); code != 409 {
		t.Errorf("second start: got %d, want 409 (ErrAlreadyActive)", code)
	}

	// Pause.
	if code, body := e.adminPost("/api/admin/proctoring/pause", ""); code != 200 {
		t.Errorf("pause: got %d, want 200 (body: %s)", code, body)
	} else {
		s := decodeProctoringState(t, body)
		if !s.IsPaused() {
			t.Errorf("after pause: state = %+v, want paused", s)
		}
		if s.SessionId != sessionId {
			t.Errorf("sessionId changed on pause: %q → %q", sessionId, s.SessionId)
		}
	}

	// Second pause must conflict.
	if code, _ := e.adminPost("/api/admin/proctoring/pause", ""); code != 409 {
		t.Errorf("second pause: got %d, want 409 (ErrNotActive)", code)
	}

	// Resume.
	if code, body := e.adminPost("/api/admin/proctoring/resume", ""); code != 200 {
		t.Errorf("resume: got %d, want 200 (body: %s)", code, body)
	} else {
		s := decodeProctoringState(t, body)
		if !s.IsActive() {
			t.Errorf("after resume: state = %+v, want active", s)
		}
	}

	// Stop.
	if code, body := e.adminPost("/api/admin/proctoring/stop", ""); code != 200 {
		t.Errorf("stop: got %d, want 200 (body: %s)", code, body)
	} else {
		s := decodeProctoringState(t, body)
		if !s.IsIdle() {
			t.Errorf("after stop: state = %+v, want idle", s)
		}
	}

	// Operations on idle session must conflict.
	for _, op := range []string{"pause", "resume", "stop"} {
		if code, _ := e.adminPost("/api/admin/proctoring/"+op, ""); code != 409 {
			t.Errorf("%s on idle session: got %d, want 409", op, code)
		}
	}
}

// TestProctoringLifecycle_StartRejectsInvalidConfig: bogus parameters are
// rejected at the API boundary with 400.
func TestProctoringLifecycle_StartRejectsInvalidConfig(t *testing.T) {
	e := newTestEnv(t)
	cases := []struct {
		name string
		body string
	}{
		{"zero-fps", `{"chunkDurationMs":1000,"fps":0,"videoBitrate":100000}`},
		{"huge-fps", `{"chunkDurationMs":1000,"fps":9999,"videoBitrate":100000}`},
		{"tiny-chunk", `{"chunkDurationMs":10,"fps":5,"videoBitrate":100000}`},
		{"huge-bitrate", `{"chunkDurationMs":1000,"fps":5,"videoBitrate":999999999}`},
		{"endsAt-in-past", `{"chunkDurationMs":1000,"fps":5,"videoBitrate":100000,"endsAt":"2020-01-01T00:00:00Z"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, _ := e.adminPost("/api/admin/proctoring/start", c.body); code != 400 {
				t.Errorf("start with %s: got %d, want 400", c.name, code)
			}
		})
	}
}

// TestProctoringAdmin_GetReturnsSessionList: after a start/stop cycle the
// admin GET endpoint still lists the now-finalized session so the UI can
// show historical recordings.
func TestProctoringAdmin_GetReturnsSessionList(t *testing.T) {
	e := newTestEnv(t)

	status, body := e.adminPost("/api/admin/proctoring/start", `{"chunkDurationMs":1000,"fps":5,"videoBitrate":100000}`)
	if status != 200 {
		t.Fatalf("start: %d %s", status, body)
	}
	firstSession := decodeProctoringState(t, body).SessionId

	// Upload one chunk for each session so both session directories exist
	// on disk (otherwise the admin GET endpoint has nothing to list).
	firstTok := e.signToken(proctoringScope(firstSession, testPeer, testStream))
	e.proctoringUpload(testPeer, firstSession, testStream, 0, 0, []byte("x"), firstTok, -1)

	if code, _ := e.adminPost("/api/admin/proctoring/stop", ""); code != 200 {
		t.Fatalf("stop: %d", code)
	}

	// newSessionId() is second-granular, so two starts within the same
	// wall-clock second would collide. Sleep across the boundary.
	time.Sleep(1100 * time.Millisecond)

	// Start a new session.
	status, body = e.adminPost("/api/admin/proctoring/start", `{"chunkDurationMs":1000,"fps":5,"videoBitrate":100000}`)
	if status != 200 {
		t.Fatalf("start #2: %d %s", status, body)
	}
	activeSession := decodeProctoringState(t, body).SessionId
	if activeSession == firstSession {
		t.Fatalf("second start returned the same sessionId %q", activeSession)
	}
	activeTok := e.signToken(proctoringScope(activeSession, testPeer, testStream))
	e.proctoringUpload(testPeer, activeSession, testStream, 0, 0, []byte("y"), activeTok, -1)

	// GET must list both sessions with the right isActive flag.
	code, getBody := e.adminGet("/api/admin/proctoring")
	if code != 200 {
		t.Fatalf("GET proctoring: %d", code)
	}
	var resp struct {
		State struct {
			SessionId string `json:"sessionId"`
			Active    bool   `json:"active"`
		} `json:"state"`
		Sessions []struct {
			SessionId string `json:"sessionId"`
			IsActive  bool   `json:"isActive"`
			Finalized bool   `json:"finalized"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(getBody), &resp); err != nil {
		t.Fatalf("decode proctoring GET: %v", err)
	}
	if resp.State.SessionId != activeSession {
		t.Errorf("active session in GET = %q, want %q", resp.State.SessionId, activeSession)
	}
	found := map[string]bool{}
	for _, s := range resp.Sessions {
		found[s.SessionId] = true
		if s.SessionId == activeSession && !s.IsActive {
			t.Errorf("active session %q not marked IsActive", s.SessionId)
		}
		if s.SessionId == firstSession && s.IsActive {
			t.Errorf("previous session %q should not be IsActive", s.SessionId)
		}
	}
	if !found[firstSession] {
		t.Errorf("first session %q not in GET response: %+v", firstSession, resp.Sessions)
	}
	if !found[activeSession] {
		t.Errorf("active session %q not in GET response: %+v", activeSession, resp.Sessions)
	}
}

// decodeProctoringState unmarshals a State body returned by admin endpoints.
func decodeProctoringState(t *testing.T, body string) (st proctoringStateView) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatalf("decode proctoring state: %v (body: %s)", err, body)
	}
	return
}

// proctoringStateView mirrors proctoring.State with the derived Active /
// Paused flags so tests can use the same predicates the JSON client uses.
type proctoringStateView struct {
	SessionId string `json:"sessionId"`
	Status    string `json:"status"`
	Active    bool   `json:"active"`
	Paused    bool   `json:"paused"`
}

func (s proctoringStateView) IsActive() bool { return s.Active || s.Status == "active" }
func (s proctoringStateView) IsPaused() bool { return s.Paused || s.Status == "paused" }
func (s proctoringStateView) IsIdle() bool   { return s.SessionId == "" || (!s.Active && !s.Paused && s.Status != "active" && s.Status != "paused") }
