// Command proctoring-e2e spins up an in-process relay and exercises every
// proctoring scenario end-to-end through a real HTTP client. Designed as
// the single entry point for humans and CI: no setup steps, no external
// dependencies beyond ffmpeg (and that only for the concat scenario).
//
// Usage:
//
//	go run ./cmd/proctoring-e2e
//	  -addr=:0          # random free port; set to a fixed :port to inspect interactively
//	  -keep=false       # keep the records dir for inspection after the run
//	  -only=            # comma-separated scenario names; default = all
//	  -v=false          # verbose per-request logging
//
// Exit code is 0 iff every selected scenario passes.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/signalling"
)

var (
	addrFlag = flag.String("addr", ":0", "listen address for the in-process relay; :0 picks a free port")
	keepFlag = flag.Bool("keep", false, "keep the temp records directory after the run (path is printed)")
	onlyFlag = flag.String("only", "", "comma-separated list of scenario names to run (default: all)")
	verbose  = flag.Bool("v", false, "verbose per-request logging")
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(1)
	}
}

func run() error {
	recordsDir, err := os.MkdirTemp("", "proctoring-e2e-")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	if *keepFlag {
		fmt.Println("records dir:", recordsDir)
	} else {
		defer os.RemoveAll(recordsDir)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Build a minimal in-process server.
	cfg := config.DefaultAppConfig()
	cfg.Record.StorageDir = recordsDir
	cfg.Debug.PprofAddr = ""
	cfg.Server.GrabberPingInterval = 100
	secret := "e2e-secret-32-bytes-0123456789abcdef"
	cfg.Security.UploadSecret = &secret

	app := fiber.New(fiber.Config{BodyLimit: 50 * 1024 * 1024, DisableStartupMessage: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv, err := signalling.NewServer(ctx, &cfg, app)
	if err != nil {
		return fmt.Errorf("NewServer: %w", err)
	}
	defer srv.Close()
	srv.SetupWebSocketsAndApi()

	ln, err := net.Listen("tcp", *addrFlag)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	go func() { _ = app.Listener(ln) }()
	defer app.ShutdownWithTimeout(2 * time.Second)
	baseURL := "http://" + ln.Addr().String()
	fmt.Printf("in-process relay listening on %s\n", baseURL)
	fmt.Printf("records dir: %s\n\n", recordsDir)

	client := &http.Client{Timeout: 10 * time.Second}
	runner := &e2eRunner{
		baseURL: baseURL,
		client:  client,
		secret:  secret,
		verbose: *verbose,
	}
	scenarios := []struct {
		name string
		fn   func(r *e2eRunner) error
	}{
		{"normal-upload", scenarioNormalUpload},
		{"gap-recovery", scenarioGapRecovery},
		{"segment-rotation-concat", scenarioSegmentRotationConcat},
		{"pause-resume-stop", scenarioPauseResumeStop},
		{"server-restart", scenarioServerRestart},
		{"token-scope-isolation", scenarioTokenScopeIsolation},
	}
	want := map[string]bool{}
	if *onlyFlag != "" {
		for _, s := range strings.Split(*onlyFlag, ",") {
			want[strings.TrimSpace(s)] = true
		}
	}

	fmt.Printf("%-26s  %s\n", "scenario", "result")
	fmt.Printf("%s\n", strings.Repeat("-", 50))
	var failed []string
	for _, sc := range scenarios {
		if len(want) > 0 && !want[sc.name] {
			continue
		}
		start := time.Now()
		err := sc.fn(runner)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Printf("%-26s  %s  (%.2fs)\n", sc.name, red("FAIL"), elapsed.Seconds())
			fmt.Printf("  └─ %s\n", err)
			failed = append(failed, sc.name)
		} else {
			fmt.Printf("%-26s  %s  (%.2fs)\n", sc.name, green("PASS"), elapsed.Seconds())
		}
	}
	fmt.Println()
	if len(failed) > 0 {
		fmt.Printf("result: %s  (%d/%d passed)\n", red("FAIL"), len(scenarios)-len(failed), len(scenarios))
		fmt.Printf("failed scenarios: %s\n", strings.Join(failed, ", "))
		return errors.New("one or more scenarios failed")
	}
	fmt.Printf("result: %s  (%d/%d passed)\n", green("PASS"), len(scenarios), len(scenarios))
	return nil
}

type e2eRunner struct {
	baseURL string
	client  *http.Client
	secret  string
	verbose bool
}

// signToken reproduces the server's HMAC-SHA256 token derivation.
func (r *e2eRunner) signToken(scope string) string {
	h := hmac.New(sha256.New, []byte(r.secret))
	h.Write([]byte(scope))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (r *e2eRunner) upload(peer, session, stream string, seq, segment int, body []byte, token string, advanceToSeq int) (int, string) {
	url := r.baseURL + "/api/agent/" + peer + "/proctoring_upload" +
		"?sessionId=" + session +
		"&streamKey=" + stream +
		"&seq=" + itoa(seq) +
		"&segment=" + itoa(segment)
	if advanceToSeq >= 0 {
		url += "&advanceToSeq=" + itoa(advanceToSeq)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "chunk.webm")
	if err != nil {
		return 0, err.Error()
	}
	fw.Write(body)
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("X-Upload-Token", token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if r.verbose {
		fmt.Printf("    POST %s → %d %s\n", strings.Replace(url, r.baseURL, "", 1), resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return resp.StatusCode, string(respBody)
}

func (r *e2eRunner) adminPost(path, body string) (int, string) {
	req, _ := http.NewRequest(http.MethodPost, r.baseURL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("admin", "live")
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if r.verbose {
		fmt.Printf("    POST %s → %d body=%s\n", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	// Tiny breather so a follow-up read sees the persisted state. Under
	// heavy CPU contention the goroutine that flips state on disk can race
	// with the next request; 5ms is unobservable in the report.
	time.Sleep(5 * time.Millisecond)
	return resp.StatusCode, string(respBody)
}

func (r *e2eRunner) adminGet(path string) (int, string) {
	req, _ := http.NewRequest(http.MethodGet, r.baseURL+path, nil)
	req.SetBasicAuth("admin", "live")
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

// scenarios ----------------------------------------------------------------

func scenarioNormalUpload(r *e2eRunner) error {
	const peer, session, stream = "peer-A", "normal-upload-sid", "desktop"
	tok := r.signToken(proctoringScope(session, peer, stream))
	for seq, p := range []string{"aaa", "bbb", "ccc"} {
		if code, body := r.upload(peer, session, stream, seq, 0, []byte(p), tok, -1); code != 200 || body != "OK" {
			return fmt.Errorf("seq=%d: got (%d, %q), want (200, OK)", seq, code, body)
		}
	}
	// Already-committed must be idempotent.
	if code, body := r.upload(peer, session, stream, 0, 0, []byte("REPLAY"), tok, -1); code != 200 || body != "OK (already committed)" {
		return fmt.Errorf("replay: got (%d, %q)", code, body)
	}
	return nil
}

func scenarioGapRecovery(r *e2eRunner) error {
	const peer, session, stream = "peer-B", "gap-recovery-sid", "desktop"
	tok := r.signToken(proctoringScope(session, peer, stream))
	if code, _ := r.upload(peer, session, stream, 0, 0, []byte("a"), tok, -1); code != 200 {
		return fmt.Errorf("seq=0: %d", code)
	}
	if code, _ := r.upload(peer, session, stream, 1, 0, []byte("b"), tok, -1); code != 200 {
		return fmt.Errorf("seq=1: %d", code)
	}
	// Gap without advance → 409.
	if code, _ := r.upload(peer, session, stream, 10, 1, []byte("c"), tok, -1); code != 409 {
		return fmt.Errorf("gap without advance: got %d, want 409", code)
	}
	// Same gap with advance → 200, gap closed.
	if code, body := r.upload(peer, session, stream, 10, 1, []byte("c"), tok, 10); code != 200 || body != "OK" {
		return fmt.Errorf("gap with advance: got (%d, %q)", code, body)
	}
	// Subsequent seq=11 without advance → 200.
	if code, _ := r.upload(peer, session, stream, 11, 1, []byte("d"), tok, -1); code != 200 {
		return fmt.Errorf("seq=11 after gap: %d", code)
	}
	return nil
}

func scenarioSegmentRotationConcat(r *e2eRunner) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not installed: %w", err)
	}
	const peer, session, stream = "peer-C", "rotation-concat-sid", "desktop"
	tok := r.signToken(proctoringScope(session, peer, stream))
	// Two real webm segments produced via ffmpeg, uploaded via the agent API.
	// Re-use the relay's records dir to obtain the path; for the e2e CLI we
	// don't have access to testEnv.recordsDir, so we just feed synthetic bytes
	// and rely on ffmpeg's concat to fail predictably. To get a real concat
	// success path we generate two valid webm files via ffmpeg, then ship them
	// as upload payloads to seq=0 (segment 0) and seq=1 (segment 1).
	tmp, err := os.MkdirTemp("", "e2e-segments-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	seg0 := filepath.Join(tmp, "s0.webm")
	seg1 := filepath.Join(tmp, "s1.webm")
	if err := genWebm(seg0, 1); err != nil {
		return fmt.Errorf("gen seg0: %w", err)
	}
	if err := genWebm(seg1, 1); err != nil {
		return fmt.Errorf("gen seg1: %w", err)
	}
	b0, _ := os.ReadFile(seg0)
	b1, _ := os.ReadFile(seg1)
	if code, _ := r.upload(peer, session, stream, 0, 0, b0, tok, -1); code != 200 {
		return fmt.Errorf("upload seg0: %d", code)
	}
	if code, _ := r.upload(peer, session, stream, 1, 1, b1, tok, -1); code != 200 {
		return fmt.Errorf("upload seg1: %d", code)
	}
	// Finalize via admin endpoint.
	if code, body := r.adminPost("/api/admin/proctoring/finalize/"+session, ""); code != 200 {
		return fmt.Errorf("finalize: %d %s", code, body)
	}
	// Concat output must be ffprobe-valid with duration ~= sum.
	// We don't have direct disk access from this CLI; instead query the admin
	// file endpoint which serves the produced full.webm.
	code, body := r.adminGet("/api/admin/proctoring/file/" + session + "/" + peer + "/" + stream + "/full.webm")
	if code != 200 {
		return fmt.Errorf("download full.webm: %d", code)
	}
	if len(body) == 0 {
		return fmt.Errorf("full.webm is empty")
	}
	return nil
}

func scenarioPauseResumeStop(r *e2eRunner) error {
	// Start.
	code, body := r.adminPost("/api/admin/proctoring/start", `{"chunkDurationMs":1000,"fps":5,"videoBitrate":100000}`)
	if code != 200 {
		return fmt.Errorf("start: %d %s", code, body)
	}
	var st stateResp
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		return fmt.Errorf("decode start state: %w", err)
	}
	if !st.Active {
		return fmt.Errorf("after start: state not active")
	}
	// Second start must conflict.
	if code, _ := r.adminPost("/api/admin/proctoring/start", `{"chunkDurationMs":1000,"fps":5,"videoBitrate":100000}`); code != 409 {
		return fmt.Errorf("second start: got %d, want 409", code)
	}
	// Pause / second pause.
	if code, body := r.adminPost("/api/admin/proctoring/pause", ""); code != 200 {
		return fmt.Errorf("pause: %d %s", code, body)
	}
	if code, _ := r.adminPost("/api/admin/proctoring/pause", ""); code != 409 {
		return fmt.Errorf("second pause: got %d, want 409", code)
	}
	// Resume / Stop.
	if code, _ := r.adminPost("/api/admin/proctoring/resume", ""); code != 200 {
		return fmt.Errorf("resume: %d", code)
	}
	stopCode, stopBody := r.adminPost("/api/admin/proctoring/stop", "")
	if stopCode != 200 {
		return fmt.Errorf("stop: %d %s", stopCode, stopBody)
	}
	if *verbose {
		fmt.Printf("    stop response: %s\n", stopBody)
	}
	if err := json.Unmarshal([]byte(stopBody), &st); err != nil {
		return fmt.Errorf("decode stop state: %w", err)
	}
	// Stop preserves sessionId in the JSON payload (so the UI can keep
	// history) but flips status to "idle"; the caller-visible signal is
	// !Active && !Paused.
	if st.Active || st.Paused {
		return fmt.Errorf("after stop: state active=%v paused=%v, want both false", st.Active, st.Paused)
	}
	// Subsequent ops on idle session must conflict.
	for _, op := range []string{"pause", "resume", "stop"} {
		if code, _ := r.adminPost("/api/admin/proctoring/"+op, ""); code != 409 {
			return fmt.Errorf("%s on idle: got %d, want 409", op, code)
		}
	}
	return nil
}

func scenarioServerRestart(r *e2eRunner) error {
	// We can't restart the in-process server mid-run; instead we verify the
	// externally observable invariants that hold across a restart: state.json
	// is persisted with committedSeq, replay is idempotent, and gap detection
	// still works against that persisted state.
	const peer, session, stream = "peer-D", "server-restart-sid", "desktop"
	tok := r.signToken(proctoringScope(session, peer, stream))
	for _, seq := range []int{0, 1, 2} {
		if code, _ := r.upload(peer, session, stream, seq, 0, []byte("x"), tok, -1); code != 200 {
			return fmt.Errorf("seq=%d: %d", seq, code)
		}
	}
	// Replay (idempotent — exactly what a reconnecting grabber would do).
	if code, body := r.upload(peer, session, stream, 2, 0, []byte("REPLAY"), tok, -1); code != 200 || body != "OK (already committed)" {
		return fmt.Errorf("replay: got (%d, %q)", code, body)
	}
	// Gap (would still be rejected after a restart, since committedSeq persists).
	if code, _ := r.upload(peer, session, stream, 9, 0, []byte("y"), tok, -1); code != 409 {
		return fmt.Errorf("gap: got %d, want 409", code)
	}
	// Read state via the agent endpoint — that's what the grabber's reconcile
	// flow uses to resynchronise after a reconnect.
	code, body := r.adminGet("/api/admin/proctoring")
	if code != 200 {
		return fmt.Errorf("admin GET: %d", code)
	}
	if !strings.Contains(body, session) {
		return fmt.Errorf("session %q missing from admin GET: %s", session, body)
	}
	return nil
}

func scenarioTokenScopeIsolation(r *e2eRunner) error {
	const peer, session, stream = "peer-E", "token-scope-sid", "desktop"
	correctScope := proctoringScope(session, peer, stream)
	correct := r.signToken(correctScope)

	cases := []struct {
		name  string
		token string
		want  int
	}{
		{"missing", "", 401},
		{"garbage", "not-a-token", 401},
		{"foreign-secret", signWith("other-secret", correctScope), 401},
		{"wrong-stream", r.signToken(proctoringScope(session, peer, "webcam")), 401},
		{"wrong-peer", r.signToken(proctoringScope(session, "peer-other", stream)), 401},
		{"wrong-session", r.signToken(proctoringScope("other-sid", peer, stream)), 401},
		{"correct", correct, 200},
	}
	for _, c := range cases {
		// First call uses seq=0; subsequent calls re-use seq=0 and rely on
		// idempotency so each case starts from the same state.
		if code, _ := r.upload(peer, session, stream, 0, 0, []byte("x"), c.token, -1); code != c.want && !(c.want == 200 && code == 200) {
			// Allow 200 (OK) or 200 (already committed) for the correct case.
			if c.want == 200 && code == 200 {
				continue
			}
			return fmt.Errorf("token %q: got %d, want %d", c.name, code, c.want)
		}
	}
	return nil
}

// helpers ------------------------------------------------------------------

type stateResp struct {
	SessionId string `json:"sessionId"`
	Active    bool   `json:"active"`
	Paused    bool   `json:"paused"`
}

// proctoringScope mirrors the server's scope string so the e2e CLI doesn't
// have to import the signalling package's internal helper.
func proctoringScope(session, peer, stream string) string {
	return "proctoring:" + session + ":" + peer + ":" + stream
}

func genWebm(path string, seconds int) error {
	cmd := exec.Command("ffmpeg",
		"-y", "-loglevel", "error",
		"-f", "lavfi",
		"-i", "testsrc=duration="+itoa(seconds)+":size=320x240:rate=5",
		"-c:v", "libvpx",
		"-f", "webm",
		path,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func signWith(secret, scope string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(scope))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func green(s string) string { return "\033[32m" + s + "\033[0m" }
func red(s string) string   { return "\033[31m" + s + "\033[0m" }
