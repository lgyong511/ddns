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
	"sync"
	"testing"
	"time"
)

type fakeCloudOperator struct {
	mu           sync.Mutex
	records      []provider.Record
	deleted      []string
	created      []string
	createdIDs   []string
	deleteErr    error
	createErr    error
	deleteCount  int
	failOnDelete int
}

type blockingCloudOperator struct {
	started chan struct{}
}

func (f *blockingCloudOperator) GetAll(context.Context, string, provider.Version) ([]provider.Record, error) {
	return nil, nil
}

func (f *blockingCloudOperator) GetSub(ctx context.Context, _ string, _ provider.Version) ([]provider.Record, error) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *blockingCloudOperator) Delete(context.Context, string, string) error { return nil }

func (f *blockingCloudOperator) Create(context.Context, *provider.Record) (*provider.Record, error) {
	return nil, nil
}

func (f *fakeCloudOperator) GetAll(context.Context, string, provider.Version) ([]provider.Record, error) {
	return nil, nil
}

func (f *fakeCloudOperator) GetSub(context.Context, string, provider.Version) ([]provider.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]provider.Record(nil), f.records...), nil
}

func (f *fakeCloudOperator) Delete(_ context.Context, recordID, domain string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCount++
	if f.failOnDelete > 0 && f.deleteCount == f.failOnDelete {
		return f.deleteErr
	}
	if f.deleteErr != nil {
		return nil
	}
	f.deleted = append(f.deleted, recordID+"@"+domain)
	return nil
}

func (f *fakeCloudOperator) Create(_ context.Context, record *provider.Record) (*provider.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, record.RecordId+"@"+record.DomainName)
	f.createdIDs = append(f.createdIDs, record.RecordId)
	return record, nil
}

func (f *fakeCloudOperator) deletedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deleted)
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
		{RecordId: "a-record", DomainName: "example.com", RR: "nas", Type: "A"},
		{RecordId: "aaaa-record", DomainName: "example.com", RR: "nas", Type: "AAAA"},
	}}
	record := config.Record{IPVersion: provider.IPv4, SubDomains: []string{"nas.example.com"}}

	if _, err := deleteCloudRecords(context.Background(), operator, record); err != nil {
		t.Fatal(err)
	}
	if len(operator.deleted) != 1 || operator.deleted[0] != "a-record@example.com" {
		t.Fatalf("deleted records = %v, want [a-record@example.com]", operator.deleted)
	}
}

func TestDeleteCloudRecordsMatchesExactRecord(t *testing.T) {
	operator := &fakeCloudOperator{records: []provider.Record{
		{RecordId: "target", DomainName: "example.com", RR: "nas", Type: "A"},
		{RecordId: "other-name", DomainName: "example.com", RR: "backup", Type: "A"},
		{RecordId: "other-type", DomainName: "example.com", RR: "nas", Type: "AAAA"},
	}}
	record := config.Record{IPVersion: provider.IPv4, SubDomains: []string{"nas.example.com"}}

	if _, err := deleteCloudRecords(context.Background(), operator, record); err != nil {
		t.Fatal(err)
	}
	if len(operator.deleted) != 1 || operator.deleted[0] != "target@example.com" {
		t.Fatalf("deleted records = %v, want [target@example.com]", operator.deleted)
	}
}

func TestDeleteCloudRecordsRejectsAmbiguousMatches(t *testing.T) {
	operator := &fakeCloudOperator{records: []provider.Record{
		{RecordId: "first", DomainName: "example.com", RR: "nas", Type: "A"},
		{RecordId: "second", DomainName: "example.com", RR: "nas", Type: "A"},
	}}
	record := config.Record{IPVersion: provider.IPv4, SubDomains: []string{"nas.example.com"}}

	if _, err := deleteCloudRecords(context.Background(), operator, record); err == nil {
		t.Fatal("deleteCloudRecords accepted ambiguous cloud records")
	}
	if len(operator.deleted) != 0 {
		t.Fatalf("deleted records = %v, want no deletions", operator.deleted)
	}
}

func TestDeleteCloudRecordsRestoresAfterPartialFailure(t *testing.T) {
	operator := &fakeCloudOperator{
		records: []provider.Record{
			{RecordId: "first", DomainName: "example.com", RR: "one", Type: "A"},
			{RecordId: "second", DomainName: "example.com", RR: "two", Type: "A"},
		},
		deleteErr:    fmt.Errorf("delete failed"),
		failOnDelete: 2,
	}
	record := config.Record{IPVersion: provider.IPv4, SubDomains: []string{"one.example.com", "two.example.com"}}

	if _, err := deleteCloudRecords(context.Background(), operator, record); err == nil {
		t.Fatal("deleteCloudRecords accepted a failed deletion")
	}
	if len(operator.created) != 1 || operator.created[0] != "@example.com" {
		t.Fatalf("created records = %v, want [@example.com]", operator.created)
	}
	if len(operator.createdIDs) != 1 || operator.createdIDs[0] != "" {
		t.Fatalf("created record IDs = %v, want [empty]", operator.createdIDs)
	}
}

func TestCloudCleanupWorkerProcessesAndStopsJobs(t *testing.T) {
	operator := &fakeCloudOperator{records: []provider.Record{{RecordId: "record", DomainName: "example.com", RR: "nas", Type: "A"}}}
	server, err := New(Options{CloudOperatorFactory: func(config.Provider) (CloudOperator, error) { return operator, nil }})
	if err != nil {
		t.Fatal(err)
	}
	server.cloudCleanupQueue <- cloudCleanupJob{
		provider: config.Provider{Name: "home", Provider: "aliyun"},
		record:   config.Record{Name: "nas", SubDomains: []string{"nas.example.com"}, IPVersion: provider.IPv4},
	}
	deadline := time.After(time.Second)
	for operator.deletedCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("cleanup job was not processed")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := server.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestServerCloseCancelsCloudCleanup(t *testing.T) {
	operator := &blockingCloudOperator{started: make(chan struct{}, 1)}
	server, err := New(Options{CloudOperatorFactory: func(config.Provider) (CloudOperator, error) { return operator, nil }})
	if err != nil {
		t.Fatal(err)
	}
	server.cloudCleanupQueue <- cloudCleanupJob{
		provider: config.Provider{Name: "home", Provider: "aliyun"},
		record:   config.Record{Name: "nas", SubDomains: []string{"nas.example.com"}, IPVersion: provider.IPv4},
	}
	select {
	case <-operator.started:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWebCloneConfigDeepCopiesSubDomains(t *testing.T) {
	cfg := config.Config{Providers: []config.Provider{{Records: []config.Record{{SubDomains: []string{"nas.example.com"}}}}}}
	clone := cloneConfig(cfg)
	clone.Providers[0].Records[0].SubDomains[0] = "changed.example.com"
	if cfg.Providers[0].Records[0].SubDomains[0] != "nas.example.com" {
		t.Fatalf("source subdomain was mutated: %q", cfg.Providers[0].Records[0].SubDomains[0])
	}
}

func TestLoginLimiterCleansExpiredStatesAndTrimsCapacity(t *testing.T) {
	limiter := newLoginLimiter()
	now := time.Date(2026, time.August, 13, 0, 0, 0, 0, time.UTC)
	limiter.attempts["expired"] = loginAttempt{failures: 1, lastSeen: now.Add(-loginStateTTL - time.Minute)}
	if _, locked := limiter.check("expired", now); locked {
		t.Fatal("expired state is still active")
	}
	if _, ok := limiter.attempts["expired"]; ok {
		t.Fatal("expired state was not removed")
	}
	for i := 0; i < loginMaxStates+1; i++ {
		limiter.failure(fmt.Sprintf("client-%d", i), now)
	}
	if len(limiter.attempts) > loginMaxStates {
		t.Fatalf("limiter state count = %d, want at most %d", len(limiter.attempts), loginMaxStates)
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
