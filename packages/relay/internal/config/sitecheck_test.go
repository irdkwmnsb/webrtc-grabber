package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSiteCheckConfig(t *testing.T) {
	dir := t.TempDir()
	yaml := "sites:\n  - \"https://google.com\"\n  - \"https://chat.openai.com\"\nintervalMs: 8000\n"
	if err := os.WriteFile(filepath.Join(dir, "sitecheck.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadAppConfig(dir)
	if err != nil {
		t.Fatalf("LoadAppConfig: %v", err)
	}
	if len(cfg.SiteCheck.Sites) != 2 || cfg.SiteCheck.Sites[0] != "https://google.com" {
		t.Fatalf("unexpected sites: %#v", cfg.SiteCheck.Sites)
	}
	if cfg.SiteCheck.IntervalMs != 8000 {
		t.Fatalf("interval = %d, want 8000", cfg.SiteCheck.IntervalMs)
	}
}

func TestSiteCheckDefaultsWhenAbsent(t *testing.T) {
	cfg, err := LoadAppConfig(t.TempDir()) // no sitecheck.yaml
	if err != nil {
		t.Fatalf("LoadAppConfig: %v", err)
	}
	if len(cfg.SiteCheck.Sites) != 0 {
		t.Fatalf("expected no sites by default, got %#v", cfg.SiteCheck.Sites)
	}
	if cfg.SiteCheck.IntervalMs != 15000 {
		t.Fatalf("default interval = %d, want 15000", cfg.SiteCheck.IntervalMs)
	}
}
