package signalling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
)

// newAuthEnv builds a server with auth enabled, one admin (admin/live) and one
// student participant (001/sekret).
func newAuthEnv(t *testing.T) *testEnv {
	t.Helper()
	cfg := config.DefaultAppConfig()
	cfg.Record.StorageDir = t.TempDir()
	cfg.Debug.PprofAddr = ""
	secret := "test-secret-32-bytes-0123456789abcdef"
	cfg.Security.UploadSecret = &secret
	cfg.Security.AuthEnabled = true
	cfg.Security.Participants = []config.ParticipantInfo{
		{Name: "001", TeamName: "Team 001", Password: "sekret"},
		{Name: "002", TeamName: "Team 002"}, // no password -> can't log in
	}

	app := fiber.New(fiber.Config{BodyLimit: 1 << 20, DisableStartupMessage: true})
	srv, err := NewServer(context.Background(), &cfg, app)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	srv.SetupWebSocketsAndApi()
	return &testEnv{t: t, srv: srv, app: app, recordsDir: cfg.Record.StorageDir, uploadSecret: secret}
}

func (e *testEnv) login(login, password string) (*http.Response, string) {
	e.t.Helper()
	body, _ := json.Marshal(loginRequest{Login: login, Password: password})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.app.Test(req, -1)
	if err != nil {
		e.t.Fatalf("app.Test: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func TestLogin_AdminSuccess(t *testing.T) {
	e := newAuthEnv(t)
	resp, body := e.login("admin", "live")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var out loginResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Role != "admin" || out.Redirect != "/admin" {
		t.Fatalf("unexpected response: %+v", out)
	}
	// Admin login must NOT set a capture cookie.
	for _, c := range resp.Cookies() {
		if c.Name == captureCookieName && c.Value != "" {
			t.Fatalf("admin login unexpectedly set capture cookie")
		}
	}
}

func TestLogin_StudentSuccessSetsCookie(t *testing.T) {
	e := newAuthEnv(t)
	resp, body := e.login("001", "sekret")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var out loginResponse
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Role != "student" || out.PeerName != "001" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if !strings.Contains(out.Redirect, "peerName=001") {
		t.Fatalf("redirect missing peerName: %s", out.Redirect)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == captureCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no capture cookie set")
	}
	if !cookie.HttpOnly {
		t.Fatal("capture cookie should be HttpOnly")
	}
	name, ok := e.srv.verifyCaptureToken(cookie.Value)
	if !ok || name != "001" {
		t.Fatalf("cookie did not verify for 001: name=%q ok=%v", name, ok)
	}
}

func TestLogin_Rejections(t *testing.T) {
	e := newAuthEnv(t)
	cases := []struct{ login, pass string }{
		{"admin", "wrong"},  // bad admin password
		{"001", "wrong"},    // bad student password
		{"002", ""},         // participant without a password
		{"002", "anything"}, // ditto
		{"ghost", "sekret"}, // unknown login
	}
	for _, tc := range cases {
		resp, body := e.login(tc.login, tc.pass)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("login(%q,%q): status = %d, body = %s", tc.login, tc.pass, resp.StatusCode, body)
		}
	}
}

func TestLogin_DisabledReturns404(t *testing.T) {
	e := newTestEnv(t) // auth disabled by default
	resp, _ := e.login("admin", "live")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when auth disabled, got %d", resp.StatusCode)
	}
}

func TestCaptureToken_Roundtrip(t *testing.T) {
	e := newAuthEnv(t)
	exp := time.Now().Add(time.Hour).Unix()
	tok := e.srv.signCaptureToken("peer.with.dots", exp)

	name, ok := e.srv.verifyCaptureToken(tok)
	if !ok || name != "peer.with.dots" {
		t.Fatalf("roundtrip failed: name=%q ok=%v", name, ok)
	}

	// Tampered signature.
	if _, ok := e.srv.verifyCaptureToken(tok + "x"); ok {
		t.Fatal("tampered token verified")
	}
	// Expired.
	old := e.srv.signCaptureToken("001", time.Now().Add(-time.Minute).Unix())
	if _, ok := e.srv.verifyCaptureToken(old); ok {
		t.Fatal("expired token verified")
	}
	// Garbage.
	if _, ok := e.srv.verifyCaptureToken("not-a-token"); ok {
		t.Fatal("garbage token verified")
	}
}

func TestCapturePage_RedirectsWithoutCookie(t *testing.T) {
	e := newAuthEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/capture?peerName=001", nil)
	resp, err := e.app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("expected redirect to /login, got %q", loc)
	}
}

func TestCapturePage_RedirectsToOwnPeerOnMismatch(t *testing.T) {
	e := newAuthEnv(t)
	tok := e.srv.signCaptureToken("001", time.Now().Add(time.Hour).Unix())
	req := httptest.NewRequest(http.MethodGet, "/capture?peerName=002", nil)
	req.AddCookie(&http.Cookie{Name: captureCookieName, Value: tok})
	resp, err := e.app.Test(req, -1)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 redirect, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "peerName=001") {
		t.Fatalf("expected redirect to own peer 001, got %q", loc)
	}
}
