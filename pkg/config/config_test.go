package config

import (
	"strings"
	"testing"

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
