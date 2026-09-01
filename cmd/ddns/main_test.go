package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ddns/pkg/config"
)

func TestResolveConfigPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		exeDir   string
		expected string
	}{
		{name: "default executable directory", exeDir: "/opt/ddns", expected: "/opt/ddns/config/config.yaml"},
		{name: "explicit absolute path", input: "/etc/ddns/custom.yaml", exeDir: "/opt/ddns", expected: "/etc/ddns/custom.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveConfigPath(tt.input, tt.exeDir)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.expected {
				t.Fatalf("resolveConfigPath() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestResolveConfigPathRelativeExplicitPathUsesWorkingDirectory(t *testing.T) {
	got, err := resolveConfigPath("./config.yaml", "/opt/ddns")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs("./config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(want) {
		t.Fatalf("resolveConfigPath() = %q, want %q", got, want)
	}
}

func TestPrepareConfigFile(t *testing.T) {
	t.Run("explicit Web path creates empty config", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "config.yaml")
		created, err := prepareConfigFile(path, true, true, "")
		if err != nil {
			t.Fatal(err)
		}
		if !created {
			t.Fatal("prepareConfigFile() did not create the Web setup config")
		}
		manager := config.NewManager()
		t.Cleanup(func() { _ = manager.Close() })
		if err := manager.Load(path); err != nil {
			t.Fatalf("loading created config: %v", err)
		}
		cfg, err := manager.Get()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Auth.PasswordHash != "" || len(cfg.Providers) != 0 {
			t.Fatalf("created config = %#v, want uninitialized Web config", cfg)
		}
	})

	t.Run("explicit non-Web path remains required", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		created, err := prepareConfigFile(path, true, false, "")
		if err == nil || !strings.Contains(err.Error(), "不存在") {
			t.Fatalf("error = %v, want missing explicit config error", err)
		}
		if created {
			t.Fatal("prepareConfigFile() created config outside Web mode")
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("config file exists or stat failed unexpectedly: %v", statErr)
		}
	})

	t.Run("existing config is preserved", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		original := []byte("providers: []\nwebhook:\n  headers: []\n# keep\n")
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		created, err := prepareConfigFile(path, true, true, "")
		if err != nil {
			t.Fatal(err)
		}
		if created {
			t.Fatal("prepareConfigFile() reported replacing existing config")
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(original) {
			t.Fatalf("existing config changed to %q", got)
		}
	})

	t.Run("directory path is rejected", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareConfigFile(path, true, true, ""); err == nil {
			t.Fatal("prepareConfigFile() accepted a directory path")
		}
	})

	t.Run("invalid existing config is not replaced", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		original := []byte("providers: [")
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareConfigFile(path, true, true, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFile(path); err == nil {
			t.Fatal("invalid existing config unexpectedly loaded")
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(original) {
			t.Fatalf("invalid existing config changed to %q", got)
		}
	})
}
