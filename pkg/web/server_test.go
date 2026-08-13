package web

import (
	"context"
	"ddns/pkg/config"
	"ddns/pkg/provider"
	"ddns/pkg/version"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestConfigReadErrorsAreReturned(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(configPath, []byte("providers: ["), 0600); err != nil {
		t.Fatal(err)
	}
	server := &Server{configPath: configPath, sessions: newSessionStore()}
	server.templates, _ = parseTemplates()

	tests := []struct {
		name    string
		handler http.Handler
	}{
		{name: "setup", handler: http.HandlerFunc(server.setup)},
		{name: "login", handler: http.HandlerFunc(server.login)},
		{name: "require auth", handler: server.requireAuth(func(http.ResponseWriter, *http.Request) {})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.name == "require auth" {
				request.URL.Path = "/protected"
			}
			response := httptest.NewRecorder()
			tt.handler.ServeHTTP(response, request)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
			}
			if strings.Contains(response.Body.String(), "首次设置") || strings.Contains(response.Body.String(), "登录") {
				t.Fatalf("handler entered an initialization or login flow: %s", response.Body.String())
			}
		})
	}
}

func TestLoginLimiterLocksAndBacksOff(t *testing.T) {
	limiter := newLoginLimiter()
	key := "192.0.2.1"
	now := time.Date(2026, time.August, 13, 0, 0, 0, 0, time.UTC)

	for attempt := 1; attempt < loginFailureThreshold; attempt++ {
		if _, locked := limiter.failure(key, now); locked {
			t.Fatalf("attempt %d unexpectedly locked the client", attempt)
		}
	}
	lockDuration, locked := limiter.failure(key, now)
	if !locked || lockDuration != loginInitialLock {
		t.Fatalf("first lock = (%v, %v), want (%v, true)", lockDuration, locked, loginInitialLock)
	}
	if retryAfter, locked := limiter.check(key, now.Add(loginInitialLock-time.Second)); !locked || retryAfter != time.Second {
		t.Fatalf("active lock = (%v, %v), want (1s, true)", retryAfter, locked)
	}

	nextAttempt := now.Add(loginInitialLock)
	for attempt := 1; attempt < loginFailureThreshold; attempt++ {
		if _, locked := limiter.failure(key, nextAttempt); locked {
			t.Fatalf("backoff attempt %d unexpectedly locked the client", attempt)
		}
	}
	lockDuration, locked = limiter.failure(key, nextAttempt)
	if !locked || lockDuration != 2*loginInitialLock {
		t.Fatalf("second lock = (%v, %v), want (%v, true)", lockDuration, locked, 2*loginInitialLock)
	}

	limiter.success(key)
	if _, locked := limiter.check(key, nextAttempt); locked {
		t.Fatal("successful login did not clear the client limiter state")
	}
}

func TestParseProviderRecordsRejectsMismatchedFields(t *testing.T) {
	form := url.Values{
		"recordName":       {"nas"},
		"recordSubDomains": {"nas.example.com"},
		"recordIPVersion":  {"4"},
		"recordTTL":        {"600"},
		"recordInterval":   {"30"},
		"recordGetType0":   {"url"},
		"recordGetValue":   {},
		"recordRule":       {""},
	}
	request := &http.Request{Form: form}

	if _, err := parseProviderRecords(request); err == nil {
		t.Fatal("parseProviderRecords accepted a missing recordGetValue field")
	} else if !strings.Contains(err.Error(), "recordGetValue") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseProviderRecordsRejectsEveryMismatchedField(t *testing.T) {
	fieldNames := []string{"recordSubDomains", "recordIPVersion", "recordTTL", "recordInterval", "recordGetValue", "recordRule"}
	for _, fieldName := range fieldNames {
		t.Run(fieldName, func(t *testing.T) {
			form := url.Values{
				"recordName":       {"nas"},
				"recordSubDomains": {"nas.example.com"},
				"recordIPVersion":  {"4"},
				"recordTTL":        {"600"},
				"recordInterval":   {"30"},
				"recordGetType0":   {"url"},
				"recordGetValue":   {"https://example.com"},
				"recordRule":       {""},
			}
			form.Del(fieldName)
			request := &http.Request{Form: form}

			if _, err := parseProviderRecords(request); err == nil {
				t.Fatalf("parseProviderRecords accepted missing %s", fieldName)
			} else if !strings.Contains(err.Error(), fmt.Sprintf("字段 %s", fieldName)) {
				t.Fatalf("unexpected error for %s: %v", fieldName, err)
			}
		})
	}
}
