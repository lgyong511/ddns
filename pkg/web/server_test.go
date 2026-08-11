package web

import (
	"context"
	"ddns/pkg/config"
	"ddns/pkg/provider"
	"ddns/pkg/version"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCloudOperator struct {
	records []provider.Record
	deleted []string
}

func (f *fakeCloudOperator) GetAll(context.Context, string, provider.Version) ([]provider.Record, error) {
	return nil, nil
}

func (f *fakeCloudOperator) GetSub(context.Context, string, provider.Version) ([]provider.Record, error) {
	return f.records, nil
}

func (f *fakeCloudOperator) Delete(_ context.Context, recordID, domain string) error {
	f.deleted = append(f.deleted, recordID+"@"+domain)
	return nil
}

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

func TestDeleteCloudRecordsFiltersRecordType(t *testing.T) {
	operator := &fakeCloudOperator{records: []provider.Record{
		{RecordId: "a-record", DomainName: "example.com", Type: "A"},
		{RecordId: "aaaa-record", DomainName: "example.com", Type: "AAAA"},
	}}
	record := config.Record{IPVersion: provider.IPv4, SubDomains: []string{"nas.example.com"}}

	if err := deleteCloudRecords(context.Background(), operator, record); err != nil {
		t.Fatal(err)
	}
	if len(operator.deleted) != 1 || operator.deleted[0] != "a-record@example.com" {
		t.Fatalf("deleted records = %v, want [a-record@example.com]", operator.deleted)
	}
}
