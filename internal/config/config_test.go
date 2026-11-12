package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsValid(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
}

func TestLocalModeRejectsWildcard(t *testing.T) {
	c := Defaults()
	c.Server.Listen = "0.0.0.0:7878"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for wildcard bind in local mode")
	}
}

func TestRemoteRequiresTLS(t *testing.T) {
	c := Defaults()
	c.Server.Mode = ModeRemote
	c.Server.Listen = "0.0.0.0:7878"
	if err := c.Validate(); err == nil {
		t.Fatal("expected TLS error")
	}
	c.Server.TLSCert, c.Server.TLSKey = "c.pem", "k.pem"
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestLoadOverridesAndRejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.toml")
	os.WriteFile(p, []byte("data_dir=\""+dir+"\"\n[server]\nshutdown_timeout=\"3s\"\n[features]\nmemory=true\n"), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.ShutdownTimeout.Std() != 3*time.Second || !c.FeatureEnabled("memory") {
		t.Fatalf("override not applied: %+v", c)
	}
	if c.Server.Listen != "127.0.0.1:7878" {
		t.Fatal("default lost")
	}

	os.WriteFile(p, []byte("bogus=1\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("expected unknown-key error")
	}
	os.WriteFile(p, []byte("[features]\nmemry=true\n"), 0o600)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "memry") {
		t.Fatalf("expected unknown feature error, got %v", err)
	}
}
