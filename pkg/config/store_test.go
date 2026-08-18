package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestManagerSavePreservesYAMLCommentsAndUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `# DDNS 主配置
providers:
  # 家庭 Provider
  - name: home
    provider: aliyun
    keyId: id
    keySecret: secret
    forceInterval: 5
    records:
      # 公网记录
      - name: nas
        subDomains: [nas.example.com]
        ipVersion: 4
        ttl: 600
        getType: url
        getValue: https://example.com
        interval: 30
        rule: ""
webhook:
  url: ""
  body: ""
  headers: []
custom:
  keep: true
`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.Load(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := manager.Get()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Webhook.URL = "https://notify.example.com"
	if err := manager.Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# DDNS 主配置", "# 家庭 Provider", "# 公网记录", "custom:", "keep: true", "url: \"https://notify.example.com\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config does not contain %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected configuration backup, err=%v", err)
	}
}

func TestManagerSaveRejectsExternalModification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("providers: []\nwebhook:\n  headers: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.Load(path); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("providers: []\nwebhook:\n  url: changed\n  headers: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := manager.Get()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Save(cfg); err == nil || !strings.Contains(err.Error(), "外部修改") {
		t.Fatalf("Save() error = %v, want external modification error", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "url: changed") {
		t.Fatalf("external configuration was overwritten:\n%s", data)
	}
}

func TestManagerSaveKeepsCommentsWithNamedItems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `providers:
  # first provider comment
  - name: first
    provider: aliyun
    keyId: id
    keySecret: secret
    records: []
  # second provider comment
  - name: second
    provider: aliyun
    keyId: id
    keySecret: secret
    records: []
webhook:
  headers: []
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	t.Cleanup(func() { _ = manager.Close() })
	if err := manager.Load(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := manager.Get()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers = cfg.Providers[1:]
	if err := manager.Save(cfg); err != nil {
		t.Fatal(err)
	}
	saved := string(mustReadFile(t, path))
	if strings.Contains(saved, "first provider comment") || !strings.Contains(saved, "second provider comment") {
		t.Fatalf("named item comments were not preserved correctly:\n%s", saved)
	}
}

func TestPrepareDefaultFileMigratesLegacyAndCreatesEmptyConfig(t *testing.T) {
	tests := []struct {
		name       string
		legacyData string
		wantLegacy bool
		wantValue  string
	}{
		{
			name: "migrates valid legacy file",
			legacyData: `# legacy
providers: []
webhook:
  headers: []
`,
			wantLegacy: true,
			wantValue:  "# legacy",
		},
		{
			name:      "creates empty file without legacy",
			wantValue: "providers: []",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config", "config.yaml")
			legacy := filepath.Join(dir, "conf.yaml")
			if tt.legacyData != "" {
				if err := os.WriteFile(legacy, []byte(tt.legacyData), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := PrepareDefaultFile(path, legacy); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tt.wantValue) {
				t.Fatalf("created config = %s, want %q", data, tt.wantValue)
			}
			if tt.wantLegacy {
				if _, err := os.Stat(legacy); err != nil {
					t.Fatalf("legacy file was removed: %v", err)
				}
			}
		})
	}
}

func TestConfigFilesUseRestrictedPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "config.yaml")
	if err := PrepareDefaultFile(path, filepath.Join(dir, "conf.yaml")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("config directory permissions = %o, want 700", got)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("config file permissions = %o, want 600", got)
	}
}

func TestWriteConfigFileHandlesRenameErrors(t *testing.T) {
	tests := []struct {
		name        string
		renameErr   error
		wantData    string
		wantErr     bool
		wantErrText string
	}{
		{
			name:      "falls back for bind mounted file",
			renameErr: &os.LinkError{Op: "rename", Err: syscall.EBUSY},
			wantData:  "updated\n",
		},
		{
			name:        "preserves file for unrelated rename error",
			renameErr:   errors.New("rename denied"),
			wantData:    "original\n",
			wantErr:     true,
			wantErrText: "rename denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte("original\n"), 0644); err != nil {
				t.Fatal(err)
			}
			err := writeConfigFileWithRename(path, []byte("updated\n"), func(oldPath, newPath string) error {
				linkErr, ok := tt.renameErr.(*os.LinkError)
				if !ok {
					return tt.renameErr
				}
				clone := *linkErr
				clone.Old = oldPath
				clone.New = newPath
				return &clone
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("writeConfigFileWithRename() error = %v, want error: %v", err, tt.wantErr)
			}
			if tt.wantErrText != "" && !strings.Contains(err.Error(), tt.wantErrText) {
				t.Fatalf("writeConfigFileWithRename() error = %v, want text %q", err, tt.wantErrText)
			}
			if got := string(mustReadFile(t, path)); got != tt.wantData {
				t.Fatalf("saved data = %q, want %q", got, tt.wantData)
			}
			if matches, err := filepath.Glob(filepath.Join(dir, ".config-*.yaml")); err != nil {
				t.Fatal(err)
			} else if len(matches) != 0 {
				t.Fatalf("temporary files were not removed: %v", matches)
			}
			if !tt.wantErr {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if got := info.Mode().Perm(); got != 0600 {
					t.Fatalf("config file permissions = %o, want 600", got)
				}
			}
		})
	}
}

func TestPrepareDefaultFileFallsBackToEmptyForInvalidLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "config.yaml")
	legacy := filepath.Join(dir, "conf.yaml")
	if err := os.WriteFile(legacy, []byte("providers: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareDefaultFile(path, legacy); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("fallback config providers = %d, want 0", len(cfg.Providers))
	}
	if _, err := os.Stat(path + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid legacy unexpectedly created migration backup, err=%v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
