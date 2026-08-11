package web

import (
	"ddns/pkg/version"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageIncludesVersion(t *testing.T) {
	server := &Server{}
	page := server.page(&http.Request{}, "测试", nil)
	if page["Version"] != version.Version {
		t.Fatalf("page version = %v, want %q", page["Version"], version.Version)
	}
}

func TestPrepareConfigFileImportsValidCandidate(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "conf.yaml")
	target := filepath.Join(dir, ".ddns_conf.yaml")
	sourceData := []byte(`providers:
    - name: home
      provider: aliyun
      keyId: id
      keySecret: secret
      forceInterval: 5
      records:
        - name: nas
          subDomains:
            - nas.example.com
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
`)
	if err := os.WriteFile(source, sourceData, 0600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareConfigFile(target, []string{source}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "name: home") || strings.Contains(text, "interval: 30ns") || strings.Contains(text, "forceInterval: 5ns") {
		t.Fatalf("unexpected imported config:\n%s", text)
	}
}
