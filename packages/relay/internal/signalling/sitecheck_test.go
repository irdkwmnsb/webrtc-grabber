package signalling

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/api"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/config"
	"github.com/irdkwmnsb/webrtc-grabber/packages/relay/internal/sockets"
)

func newSiteCheckEnv(t *testing.T, sites []string, interval int) *testEnv {
	t.Helper()
	cfg := config.DefaultAppConfig()
	cfg.Record.StorageDir = t.TempDir()
	cfg.Debug.PprofAddr = ""
	secret := "test-secret-32-bytes-0123456789abcdef"
	cfg.Security.UploadSecret = &secret
	cfg.SiteCheck.Sites = sites
	cfg.SiteCheck.IntervalMs = interval

	app := fiber.New(fiber.Config{BodyLimit: 1 << 20, DisableStartupMessage: true})
	srv, err := NewServer(context.Background(), &cfg, app)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	srv.SetupWebSocketsAndApi()
	return &testEnv{t: t, srv: srv, app: app, recordsDir: cfg.Record.StorageDir, uploadSecret: secret}
}

func TestSiteCheckClientConfig(t *testing.T) {
	// No sites -> nil (feature off).
	e := newSiteCheckEnv(t, nil, 15000)
	if cfg := e.srv.siteCheckClientConfig(); cfg != nil {
		t.Fatalf("expected nil client config when no sites, got %#v", cfg)
	}

	// Sites present -> returned; sub-second interval clamped.
	e2 := newSiteCheckEnv(t, []string{"https://example.com"}, 10)
	cfg := e2.srv.siteCheckClientConfig()
	if cfg == nil || len(cfg.Sites) != 1 {
		t.Fatalf("expected client config with sites, got %#v", cfg)
	}
	if cfg.IntervalMs != 15000 {
		t.Fatalf("expected clamped interval 15000, got %d", cfg.IntervalMs)
	}
}

func TestStoragePingStoresSiteChecks(t *testing.T) {
	e := newSiteCheckEnv(t, []string{"https://example.com"}, 5000)
	id := sockets.SocketID("1.2.3.4:5")
	e.srv.storage.addPeer("001", id)
	e.srv.storage.ping(id, &api.PeerStatus{
		SiteChecks: []api.SiteCheckResult{{Url: "https://example.com", Reachable: true}},
	})

	var found bool
	for _, p := range e.srv.storage.getAll() {
		if p.Name == "001" {
			found = true
			if len(p.SiteChecks) != 1 || !p.SiteChecks[0].Reachable {
				t.Fatalf("site checks not stored: %#v", p.SiteChecks)
			}
		}
	}
	if !found {
		t.Fatal("peer 001 not found")
	}
}

func TestSiteChecksEndpoint(t *testing.T) {
	e := newSiteCheckEnv(t, []string{"https://google.com"}, 5000)
	id := sockets.SocketID("1.2.3.4:6")
	e.srv.storage.addPeer("042", id)
	e.srv.storage.ping(id, &api.PeerStatus{
		SiteChecks: []api.SiteCheckResult{{Url: "https://google.com", Reachable: true}},
	})

	status, body := e.adminGet("/api/admin/sitechecks")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var resp siteCheckStatusResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if len(resp.Sites) != 1 || resp.Sites[0] != "https://google.com" {
		t.Fatalf("unexpected sites: %#v", resp.Sites)
	}
	var peer *siteCheckPeerStatus
	for i := range resp.Peers {
		if resp.Peers[i].Name == "042" {
			peer = &resp.Peers[i]
		}
	}
	if peer == nil {
		t.Fatalf("peer 042 missing from %#v", resp.Peers)
	}
	if !peer.Online || len(peer.SiteChecks) != 1 || !peer.SiteChecks[0].Reachable {
		t.Fatalf("unexpected peer status: %#v", peer)
	}
}

func TestProctoringSessionEndpoint(t *testing.T) {
	e := newSiteCheckEnv(t, nil, 15000)
	// Lay down a finished session on disk: one peer, one stream, a state.json.
	dir := filepath.Join(e.recordsDir, "proctoring", "sess1", "001", "desktop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	state := `{"committedSeq":3,"totalBytes":1024,"segments":[{"index":0},{"index":1}]}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}

	status, body := e.adminGet("/api/admin/proctoring/session/sess1")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var resp adminProctoringResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, body)
	}
	if len(resp.Peers) != 1 {
		t.Fatalf("expected 1 peer/stream, got %#v", resp.Peers)
	}
	p := resp.Peers[0]
	if p.PeerName != "001" || p.StreamKey != "desktop" || p.CommittedSeq != 3 {
		t.Fatalf("unexpected peer: %#v", p)
	}
}
