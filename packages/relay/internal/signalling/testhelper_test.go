package signalling

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
)

// testEnv bundles everything a proctoring test needs: an in-process Server,
// its Fiber app (driven through httptest so no real listener is required),
// the temp records directory, and helpers for signing tokens and posting
// uploads.
type testEnv struct {
	t          *testing.T
	srv        *Server
	app        *fiber.App
	recordsDir string
	uploadSecret string
}

// newTestEnv builds an in-process Server wired up with the agent + admin
// routes, pointed at a per-test records directory. All HTTP exercises go
// through app.Test() so the tests don't bind a real port.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	cfg := config.DefaultAppConfig()
	cfg.Record.StorageDir = t.TempDir()
	cfg.Debug.PprofAddr = "" // don't fight over the default pprof port
	cfg.Server.GrabberPingInterval = 100

	// Pin a stable upload secret so tests can sign tokens out-of-band.
	secret := "test-secret-32-bytes-0123456789abcdef"
	cfg.Security.UploadSecret = &secret

	app := fiber.New(fiber.Config{BodyLimit: 50 * 1024 * 1024, DisableStartupMessage: true})
	srv, err := NewServer(ctx, &cfg, app)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	srv.SetupWebSocketsAndApi()

	return &testEnv{t: t, srv: srv, app: app, recordsDir: cfg.Record.StorageDir, uploadSecret: secret}
}

// signToken reproduces the server's HMAC-SHA256 token derivation so tests
// can mint tokens without going through the admin API.
func (e *testEnv) signToken(scope string) string {
	return e2eSign(e.uploadSecret, scope)
}

// e2eSign signs scope with an arbitrary ASCII secret — used for both the
// happy path (via testEnv.uploadSecret) and negative tests that need a
// token signed with a foreign key.
func e2eSign(secret, scope string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(scope))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// proctoringUpload posts one chunk via the agent upload endpoint and returns
// (status, body). It mirrors what a grabber does in production.
func (e *testEnv) proctoringUpload(peer, session, stream string, seq, segment int, body []byte, token string, advanceToSeq int) (int, string) {
	e.t.Helper()
	return doUploadOn(e.srv, peer, session, stream, seq, segment, body, token, advanceToSeq)
}

// doUploadOn drives any Server's app directly via httptest. Shared between
// testEnv.proctoringUpload and the restart tests where each phase spins up
// its own server instance against the same on-disk records directory.
func doUploadOn(srv *Server, peer, session, stream string, seq, segment int, body []byte, token string, advanceToSeq int) (int, string) {
	url := "/api/agent/" + peer + "/proctoring_upload" +
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
	if _, err := fw.Write(body); err != nil {
		return 0, err.Error()
	}
	if err := mw.Close(); err != nil {
		return 0, err.Error()
	}

	req := httptest.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set(uploadTokenHeader, token)
	}
	resp, err := srv.app.Test(req, -1)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

// adminPost drives an admin endpoint with basic auth, returning (status, body).
func (e *testEnv) adminPost(path string, body string) (int, string) {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("admin", "live")
	return e.doRequest(req)
}

func (e *testEnv) adminGet(path string) (int, string) {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetBasicAuth("admin", "live")
	return e.doRequest(req)
}

func (e *testEnv) doRequest(req *http.Request) (int, string) {
	e.t.Helper()
	resp, err := e.app.Test(req, -1)
	if err != nil {
		e.t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// proctoringStreamDir is the on-disk path where chunks for a given peer /
// stream land. Tests use it to inspect files directly.
func (e *testEnv) proctoringStreamDir(session, peer, stream string) string {
	return filepath.Join(e.recordsDir, "proctoring", session, peer, stream)
}

// newMultipartUpload builds a POST request with a single "file" form field.
// It is shared between testEnv.proctoringUpload (which then drives it
// through app.Test) and tests that need to assemble pathological URLs by
// hand (invalid path segments, missing params, etc).
func newMultipartUpload(e *testEnv, path string, body []byte, token string) *http.Request {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "chunk.webm")
	if err != nil {
		e.t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(body); err != nil {
		e.t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		e.t.Fatalf("close multipart: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set(uploadTokenHeader, token)
	}
	return req
}

// newGet builds a GET request with an optional upload token header.
func newGet(e *testEnv, path, token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set(uploadTokenHeader, token)
	}
	return req
}

// itoa avoids importing strconv into the test helper signatures; the body
// is small and the cost negligible.
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
