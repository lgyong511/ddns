package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ddns/pkg/provider"

	"go.yaml.in/yaml/v3"
)

func TestDurationFieldsUnmarshalAsUnitIntegers(t *testing.T) {
	var cfg Config
	data := []byte(`
providers:
  - name: home
    provider: aliyun
    keyId: id
    keySecret: secret
    forceInterval: 5
    records:
      - name: nas
        subDomains: [nas.example.com]
        ipVersion: 4
        ttl: 600
        getType: url
        getValue: https://example.com
        interval: 30
`)
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers[0].ForceInterval; got != 5 {
		t.Fatalf("force interval = %d, want 5 minutes", got)
	}
	if got := cfg.Providers[0].Records[0].Interval; got != 30 {
		t.Fatalf("record interval = %d, want 30 seconds", got)
	}
}

func TestDurationFieldsRejectUnitStrings(t *testing.T) {
	var cfg Config
	data := []byte(`
providers:
  - name: home
    provider: aliyun
    keyId: id
    keySecret: secret
    forceInterval: 5m
    records:
      - name: nas
        interval: 30s
`)
	if err := yaml.Unmarshal(data, &cfg); err == nil {
		t.Fatal("unit-suffixed duration should be rejected")
	}
}

func TestDurationFieldsMarshalAsNumbers(t *testing.T) {
	cfg := Config{Providers: []Provider{{
		Name: "home", Provider: "aliyun", KeyID: "id", KeySecret: "secret", ForceInterval: 5,
		Records: []Record{{Name: "nas", SubDomains: []string{"nas.example.com"}, IPVersion: provider.IPv4, TTL: 600, GetType: "url", GetValue: "https://example.com", Interval: 30}},
	}}}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "interval: 30ns") || strings.Contains(text, "forceInterval: 5ns") {
		t.Fatalf("duration fields should not include units:\n%s", text)
	}
	if !strings.Contains(text, "forceInterval: 5") || !strings.Contains(text, "interval: 30") {
		t.Fatalf("duration fields should be plain numbers:\n%s", text)
	}
}

func TestConfigValidateStringLimits(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "provider name",
			mutate: func(cfg *Config) {
				cfg.Providers[0].Name = strings.Repeat("a", MaxProviderNameBytes+1)
			},
			wantErr: "providers[0].name",
		},
		{
			name: "record name",
			mutate: func(cfg *Config) {
				cfg.Providers[0].Records[0].Name = strings.Repeat("a", MaxRecordNameBytes+1)
			},
			wantErr: "records[0].name",
		},
		{
			name: "domain label",
			mutate: func(cfg *Config) {
				cfg.Providers[0].Records[0].SubDomains = []string{strings.Repeat("a", MaxDomainLabelBytes+1) + ".example.com"}
			},
			wantErr: "域名标签长度",
		},
		{
			name: "url value",
			mutate: func(cfg *Config) {
				cfg.Providers[0].Records[0].GetValue = strings.Repeat("a", MaxURLBytes+1)
			},
			wantErr: "getValue",
		},
		{
			name: "webhook body",
			mutate: func(cfg *Config) {
				cfg.Webhook.Body = strings.Repeat("a", MaxWebhookBodyBytes+1)
			},
			wantErr: "webhook.body",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestConfigValidateEnumerationsAndRanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"provider", func(cfg *Config) { cfg.Providers[0].Provider = "unknown" }, ".provider 无效"},
		{"get type", func(cfg *Config) { cfg.Providers[0].Records[0].GetType = "unknown" }, ".getType 无效"},
		{"ttl", func(cfg *Config) { cfg.Providers[0].Records[0].TTL = 86401 }, ".ttl 无效"},
		{"interval", func(cfg *Config) { cfg.Providers[0].Records[0].Interval = 61 }, ".interval 无效"},
		{"force interval", func(cfg *Config) { cfg.Providers[0].ForceInterval = 31 }, ".forceInterval 无效"},
		{"duid ipv4", func(cfg *Config) { cfg.Providers[0].Records[0].GetType = "duid" }, "duid 仅支持 IPv6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func validConfig() Config {
	return Config{
		Providers: []Provider{{
			Name: "home", Provider: "aliyun", KeyID: "id", KeySecret: "secret", ForceInterval: 5,
			Records: []Record{{Name: "nas", SubDomains: []string{"nas.example.com"}, IPVersion: provider.IPv4, TTL: 600, GetType: "url", GetValue: "https://example.com", Interval: 30}},
		}},
		Webhook: Webhook{Headers: []string{}},
	}
}

func TestValidateDomainNameRejectsOverlongName(t *testing.T) {
	value := strings.Repeat("a.", 126) + "com"
	if err := validateDomainName(value); err == nil {
		t.Fatal("validateDomainName accepted an overlong domain")
	} else if !strings.Contains(err.Error(), "253") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidateNormalizesSubDomains(t *testing.T) {
	cfg := validConfig()
	cfg.Providers[0].Records[0].SubDomains = []string{" WWW.Example.COM. ", "例子.测试"}

	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	want := []string{"www.example.com", "xn--fsqu00a.xn--0zwm56d"}
	for index, domain := range want {
		if got := cfg.Providers[0].Records[0].SubDomains[index]; got != domain {
			t.Fatalf("subdomain = %q, want %q", got, domain)
		}
	}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "WWW.Example.COM.") || strings.Contains(text, "例子.测试") {
		t.Fatalf("marshaled config contains unnormalized domains:\n%s", text)
	}
}

func TestConfigValidateRejectsDuplicateDomainAndVersion(t *testing.T) {
	tests := []struct {
		name          string
		secondDomain  string
		secondVersion provider.Version
		wantErr       bool
	}{
		{name: "case and trailing dot", secondDomain: "NAS.EXAMPLE.COM.", secondVersion: provider.IPv4, wantErr: true},
		{name: "unicode and IDNA", secondDomain: "XN--FSQU00A.XN--0ZWM56D", secondVersion: provider.IPv4, wantErr: true},
		{name: "different address family", secondDomain: "nas.example.com", secondVersion: provider.IPv6, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			if tt.name == "unicode and IDNA" {
				cfg.Providers[0].Records[0].SubDomains = []string{"例子.测试"}
			}
			cfg.Providers[0].Records = append(cfg.Providers[0].Records, Record{
				Name: "other", SubDomains: []string{tt.secondDomain}, IPVersion: tt.secondVersion,
				TTL: 600, GetType: "url", GetValue: "https://example.com", Interval: 30,
			})
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, want error: %v", err, tt.wantErr)
			}
		})
	}
}

func TestCloneConfigDeepCopiesSubDomains(t *testing.T) {
	cfg := validConfig()
	clone := cloneConfig(&cfg)
	clone.Providers[0].Records[0].SubDomains[0] = "changed.example.com"

	if cfg.Providers[0].Records[0].SubDomains[0] != "nas.example.com" {
		t.Fatalf("source subdomain was mutated: %q", cfg.Providers[0].Records[0].SubDomains[0])
	}
}

func TestManagerCallbacksAllowReentry(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Manager, string, *Config) error
		call  func(*Manager, *Config) error
		retry func(*Manager, *Config) error
	}{
		{
			name: "save",
			setup: func(manager *Manager, path string, _ *Config) error {
				if err := os.WriteFile(path, []byte("providers: []\nwebhook:\n  headers: []\n"), 0600); err != nil {
					return err
				}
				return manager.Load(path)
			},
			call:  func(manager *Manager, cfg *Config) error { return manager.Save(cfg) },
			retry: func(manager *Manager, cfg *Config) error { return manager.Save(cfg) },
		},
		{
			name: "reload",
			setup: func(manager *Manager, path string, cfg *Config) error {
				data, err := yaml.Marshal(cfg)
				if err != nil {
					return err
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					return err
				}
				if err := manager.Load(path); err != nil {
					return err
				}
				cfg.Webhook.URL = "https://changed.example.com"
				data, err = yaml.Marshal(cfg)
				if err != nil {
					return err
				}
				return os.WriteFile(path, data, 0600)
			},
			call:  func(manager *Manager, _ *Config) error { return manager.Reload() },
			retry: func(manager *Manager, _ *Config) error { return manager.Reload() },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewManager()
			t.Cleanup(func() { _ = manager.Close() })
			cfg := validConfig()
			if err := tt.setup(manager, filepath.Join(t.TempDir(), "config.yaml"), &cfg); err != nil {
				t.Fatal(err)
			}
			var once sync.Once
			callbackDone := make(chan error, 1)
			manager.RegCallback(func() {
				once.Do(func() { callbackDone <- tt.retry(manager, &cfg) })
			})

			callDone := make(chan error, 1)
			go func() { callDone <- tt.call(manager, &cfg) }()
			select {
			case err := <-callDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("configuration update deadlocked")
			}
			select {
			case err := <-callbackDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("reentrant callback did not finish")
			}
		})
	}
}
