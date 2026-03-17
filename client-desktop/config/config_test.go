package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeServerURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"192.168.18.240:8080", "http://192.168.18.240:8080"},
		{" http://192.168.18.240:8080/ ", "http://192.168.18.240:8080"},
		{"https://example.com", "https://example.com"},
		{"", ""},
	}

	for _, tc := range cases {
		got := NormalizeServerURL(tc.in)
		if got != tc.want {
			t.Fatalf("NormalizeServerURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSaveServerURLOnlyPreservesOtherFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	orig := &Config{
		ServerURL:      "http://old-host:8080",
		Username:       "admin",
		Password:       "secret",
		E2EEEnabled:    true,
		P2PEnabled:     true,
		StunURL:        "stun:stun.l.google.com:19302",
		AutoReconnect:  true,
		ReconnectDelay: 5,
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal orig config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatalf("write orig config: %v", err)
	}

	cfg := &Config{FilePath: cfgPath}
	if err := cfg.SaveServerURLOnly("192.168.18.240:8080"); err != nil {
		t.Fatalf("SaveServerURLOnly: %v", err)
	}

	updatedData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	var updated Config
	if err := json.Unmarshal(updatedData, &updated); err != nil {
		t.Fatalf("unmarshal updated config: %v", err)
	}

	if updated.ServerURL != "http://192.168.18.240:8080" {
		t.Fatalf("server url mismatch: got %q", updated.ServerURL)
	}
	if updated.Username != orig.Username || updated.Password != orig.Password {
		t.Fatalf("credentials should be preserved, got username=%q password=%q", updated.Username, updated.Password)
	}
}
