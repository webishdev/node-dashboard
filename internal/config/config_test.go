package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config failed: %v", err)
	}
	return path
}

func TestLoadFullConfig(t *testing.T) {
	path := writeConfig(t, `
port = 9090
update_frequency = 15
data_fetch_frequency = 3
data_dir = "/var/lib/node-dashboard"
retention_days = 7
node_exporters = ["http://node1:9100/metrics", " http://node2:9100/metrics "]

[auth]
enabled = true
user = "admin"
password = "secret"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	want := Config{
		Port:               9090,
		NodeExporters:      []string{"http://node1:9100/metrics", "http://node2:9100/metrics"},
		UpdateFrequency:    15,
		DataFetchFrequency: 3,
		DataDir:            "/var/lib/node-dashboard",
		RetentionDays:      7,
		Auth:               Auth{Enabled: true, User: "admin", Password: "secret"},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
	if got := cfg.Retention(); got != 7*24*time.Hour {
		t.Errorf("Retention = %v, want 168h", got)
	}
	if nodes := cfg.NodeConfigs(); len(nodes) != 2 || nodes[1].URL != "http://node2:9100/metrics" {
		t.Errorf("NodeConfigs = %+v", nodes)
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeConfig(t, `node_exporters = ["http://localhost:9100/metrics"]`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	want := Default()
	want.NodeExporters = []string{"http://localhost:9100/metrics"}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load = %+v, want %+v", cfg, want)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"no node exporters", `port = 8080`, "node_exporters"},
		{"blank node exporters", `node_exporters = ["  "]`, "node_exporters"},
		{"auth without credentials", "node_exporters = [\"http://a\"]\n[auth]\nenabled = true\nuser = \"admin\"", "auth.password"},
		{"unknown key", "node_exporters = [\"http://a\"]\nprot = 8080", "unknown keys: prot"},
		{"retention zero", "node_exporters = [\"http://a\"]\nretention_days = 0", "retention_days"},
		{"port out of range", "node_exporters = [\"http://a\"]\nport = 70000", "port"},
		{"invalid toml", `node_exporters = [`, "reading config"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.content))
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("Load succeeded, want error")
	}
}
