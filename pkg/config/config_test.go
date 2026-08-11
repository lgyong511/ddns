package config

import (
	"strings"
	"testing"

	"ddns/pkg/provider"

	"go.yaml.in/yaml/v3"
)

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
