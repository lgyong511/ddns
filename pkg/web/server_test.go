package web

import (
	"bytes"
	"context"
	"ddns/pkg/config"
	"ddns/pkg/provider"
	"ddns/pkg/version"
	"fmt"
	"mime/multipart"
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

type canceledRollbackOperator struct {
	records       []provider.Record
	cancel        context.CancelFunc
	deleteCalls   int
	rollbackCalls int
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

func (f *canceledRollbackOperator) GetAll(context.Context, string, provider.Version) ([]provider.Record, error) {
	return nil, nil
}

func (f *canceledRollbackOperator) GetSub(context.Context, string, provider.Version) ([]provider.Record, error) {
	return f.records, nil
}

func (f *canceledRollbackOperator) Delete(context.Context, string, string) error {
	f.deleteCalls++
	if f.deleteCalls == 2 {
		f.cancel()
		return context.Canceled
	}
	return nil
}

func (f *canceledRollbackOperator) Create(ctx context.Context, record *provider.Record) (*provider.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.rollbackCalls++
	return record, nil
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

func TestPrepareDefaultFileMigratesValidLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "conf.yaml")
	target := filepath.Join(dir, "config", "config.yaml")
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
	if err := config.PrepareDefaultFile(target, source); err != nil {
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

func TestDeleteCloudRecordsRollsBackWithCanceledDeleteContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	operator := &canceledRollbackOperator{
		records: []provider.Record{
			{RecordId: "first", DomainName: "example.com", RR: "one", Type: "A"},
			{RecordId: "second", DomainName: "example.com", RR: "two", Type: "A"},
		},
		cancel: cancel,
	}
	record := config.Record{IPVersion: provider.IPv4, SubDomains: []string{"one.example.com", "two.example.com"}}

	err := func() error {
		_, err := deleteCloudRecords(ctx, operator, record)
		return err
	}()
	if err == nil || !strings.Contains(err.Error(), "已删除 1 条，已尽力恢复基础记录；Provider 专属属性可能未保留") {
		t.Fatalf("deleteCloudRecords() error = %v, want best-effort restoration warning", err)
	}
	if operator.rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d, want 1", operator.rollbackCalls)
	}
}

func TestDeleteProviderRejectsStaleConfigVersion(t *testing.T) {
	server, configPath := newImportTestServer(t, `providers:
  - name: first
    provider: aliyun
    keyId: id
    keySecret: secret
    forceInterval: 5
    records: []
  - name: second
    provider: aliyun
    keyId: id
    keySecret: secret
    forceInterval: 5
    records: []
webhook:
  url: ""
  body: ""
  headers: []
auth: {}
`)
	token, csrf, err := server.sessions.create()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := server.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	version, err := versionConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	staleRequest := httptest.NewRequest(http.MethodPost, "/providers/0/delete", strings.NewReader(url.Values{"csrf": {csrf}, "configVersion": {"stale"}}.Encode()))
	staleRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	staleRequest.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	staleResponse := httptest.NewRecorder()
	server.deleteProvider(0).ServeHTTP(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale delete status = %d, want %d", staleResponse.Code, http.StatusConflict)
	}

	updated, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Providers) != 2 {
		t.Fatalf("stale delete changed providers: %d", len(updated.Providers))
	}

	currentRequest := httptest.NewRequest(http.MethodPost, "/providers/0/delete", strings.NewReader(url.Values{"csrf": {csrf}, "configVersion": {version}}.Encode()))
	currentRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	currentRequest.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	currentResponse := httptest.NewRecorder()
	server.deleteProvider(0).ServeHTTP(currentResponse, currentRequest)
	if currentResponse.Code != http.StatusSeeOther {
		t.Fatalf("current delete status = %d, want %d", currentResponse.Code, http.StatusSeeOther)
	}
}

func TestServerPersistsThroughSharedConfigManager(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `# shared config
providers: []
webhook:
  headers: []
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	manager := config.NewManager()
	if err := manager.Load(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server, err := New(Options{ConfigPath: path, Reloader: manager, ConfigStore: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	cfg, err := server.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Webhook.URL = "https://shared.example.com"
	if err := server.persist(&cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "# shared config") || !strings.Contains(string(saved), "shared.example.com") {
		t.Fatalf("shared manager save lost expected content:\n%s", saved)
	}
	updated, err := manager.Get()
	if err != nil {
		t.Fatal(err)
	}
	if updated.Webhook.URL != cfg.Webhook.URL {
		t.Fatalf("manager URL = %q, want %q", updated.Webhook.URL, cfg.Webhook.URL)
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

func TestDeleteRecordPersistsThenEnqueuesCloudCleanup(t *testing.T) {
	configData := `providers:
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
webhook:
  url: ""
  body: ""
  headers: []
auth: {}
`
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(configData), 0600); err != nil {
		t.Fatal(err)
	}
	operator := &fakeCloudOperator{records: []provider.Record{{RecordId: "record", DomainName: "example.com", RR: "nas", Type: "A"}}}
	server, err := New(Options{
		ConfigPath: configPath,
		CloudOperatorFactory: func(config.Provider) (CloudOperator, error) {
			return operator, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })

	token, csrf, err := server.sessions.create()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := server.readConfig()
	if err != nil {
		t.Fatal(err)
	}
	configVersion, err := versionConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"csrf":          {csrf},
		"configVersion": {configVersion},
		"deleteCloud":   {"true"},
	}
	request := httptest.NewRequest(http.MethodPost, "/providers/0/records/0/delete", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	response := httptest.NewRecorder()
	server.deleteRecord(0, 0).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d, want %d", response.Code, http.StatusSeeOther)
	}

	persisted, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Providers[0].Records) != 0 {
		t.Fatal("record was not removed from local configuration")
	}
	deadline := time.After(time.Second)
	for operator.deletedCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("cloud cleanup was not enqueued after local persistence")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func TestEnqueueCloudCleanupDropsFullOrClosedQueue(t *testing.T) {
	server := &Server{cloudCleanupQueue: make(chan cloudCleanupJob, 1)}
	job := cloudCleanupJob{provider: config.Provider{Name: "home"}, record: config.Record{Name: "nas"}}
	server.cloudCleanupQueue <- job
	server.enqueueCloudCleanup(job)
	if len(server.cloudCleanupQueue) != 1 {
		t.Fatalf("full queue length = %d, want 1", len(server.cloudCleanupQueue))
	}

	server.cloudCleanupClosed = true
	<-server.cloudCleanupQueue
	server.enqueueCloudCleanup(job)
	if len(server.cloudCleanupQueue) != 0 {
		t.Fatalf("closed queue length = %d, want 0", len(server.cloudCleanupQueue))
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

func TestServerCloseCancelsLogStream(t *testing.T) {
	server, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	streamDone := make(chan struct{})
	go func() {
		server.logsStream(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/logs/stream", nil))
		close(streamDone)
	}()

	if err := server.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-streamDone:
	case <-time.After(time.Second):
		t.Fatal("log stream did not exit after server close")
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

func TestImportConfigPreservesWebAuth(t *testing.T) {
	server, configPath := newImportTestServer(t, `providers: []
webhook:
  url: ""
  body: ""
  headers: []
auth:
  username: current-user
  passwordHash: current-hash
`)
	token, csrf, err := server.sessions.create()
	if err != nil {
		t.Fatal(err)
	}
	request := newImportRequest(t, "import.yaml", `providers:
  - name: imported
    provider: aliyun
    keyId: imported-id
    keySecret: imported-secret
    forceInterval: 5
    records: []
webhook:
  url: https://example.com/webhook
  body: ""
  headers: []
auth:
  username: replaced-user
  passwordHash: replaced-hash
`, csrf)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/?imported=1" {
		t.Fatalf("response = (%d, %q), want (303, /?imported=1)", response.Code, response.Header().Get("Location"))
	}
	updated, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Auth.Username != "current-user" || updated.Auth.PasswordHash != "current-hash" {
		t.Fatalf("auth = %#v, want current credentials", updated.Auth)
	}
	if len(updated.Providers) != 1 || updated.Providers[0].Name != "imported" {
		t.Fatalf("providers = %#v, want imported provider", updated.Providers)
	}
	if updated.Webhook.URL != "https://example.com/webhook" {
		t.Fatalf("webhook URL = %q, want imported URL", updated.Webhook.URL)
	}
}

func TestImportConfigRejectsInvalidFileWithoutSaving(t *testing.T) {
	original := `providers: []
webhook:
  url: ""
  body: ""
  headers: []
auth: {}
`
	server, configPath := newImportTestServer(t, original)
	request := newImportRequest(t, "invalid.yaml", "providers: [", "")
	response := httptest.NewRecorder()

	server.importConfig(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "解析 YAML 配置失败") {
		t.Fatalf("response = (%d, %q), want parse error page", response.Code, response.Body.String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("config changed after invalid import:\n%s", data)
	}
}

func TestImportConfigRequiresCSRFForConfiguredConsole(t *testing.T) {
	server, configPath := newImportTestServer(t, `providers: []
webhook:
  url: ""
  body: ""
  headers: []
auth:
  username: current-user
  passwordHash: current-hash
`)
	token, _, err := server.sessions.create()
	if err != nil {
		t.Fatal(err)
	}
	request := newImportRequest(t, "import.yaml", validImportYAML, "")
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	response := httptest.NewRecorder()

	server.importConfig(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	updated, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Providers) != 0 {
		t.Fatalf("providers changed after missing CSRF: %#v", updated.Providers)
	}
}

func TestImportConfigAllowsSetupWithoutCSRF(t *testing.T) {
	server, configPath := newImportTestServer(t, `providers: []
webhook:
  url: ""
  body: ""
  headers: []
auth: {}
`)
	request := newImportRequest(t, "import.yml", validImportYAML, "")
	response := httptest.NewRecorder()

	server.importConfig(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/setup?imported=1" {
		t.Fatalf("response = (%d, %q), want setup redirect", response.Code, response.Header().Get("Location"))
	}
	updated, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Providers) != 1 || updated.Auth.PasswordHash != "" {
		t.Fatalf("updated config = %#v, want imported config without auth", updated)
	}
}

func TestParseImportedConfigRejectsUnsupportedAndOversizedFiles(t *testing.T) {
	if _, err := parseImportedConfig(strings.NewReader(validImportYAML), "config.json"); err == nil {
		t.Fatal("parseImportedConfig accepted unsupported filename")
	}
	tooLarge := strings.Repeat("a", maxRequestBodyBytes+1)
	if _, err := parseImportedConfig(strings.NewReader(tooLarge), "config.yaml"); err == nil {
		t.Fatal("parseImportedConfig accepted oversized config")
	}
}

func TestImportConfigRequiresFileAndRendersWarning(t *testing.T) {
	server, configPath := newImportTestServer(t, `providers: []
webhook:
  url: ""
  body: ""
  headers: []
auth: {}
`)
	getRequest := httptest.NewRequest(http.MethodGet, "/import", nil)
	getResponse := httptest.NewRecorder()
	server.importConfig(getResponse, getRequest)
	page := getResponse.Body.String()
	if getResponse.Code != http.StatusOK || !strings.Contains(page, "无法撤销") || strings.Contains(page, "confirmOverwrite") {
		t.Fatalf("import page warning or confirmation field is incorrect: %s", page)
	}

	request := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(""))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	server.importConfig(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "读取导入表单失败") {
		t.Fatalf("response = (%d, %q), want form error page", response.Code, response.Body.String())
	}
	updated, err := loadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Providers) != 0 {
		t.Fatalf("providers changed after missing file: %#v", updated.Providers)
	}
}

func TestExportConfigExcludesAuthAndCanBeImported(t *testing.T) {
	server, _ := newImportTestServer(t, `providers:
  - name: home
    provider: aliyun
    keyId: exported-id
    keySecret: exported-secret
    forceInterval: 5
    records: []
webhook:
  url: https://example.com/webhook
  body: '{"token":"secret"}'
  headers: []
auth:
  username: current-user
  passwordHash: current-hash
`)
	token, _, err := server.sessions.create()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/export", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	response := httptest.NewRecorder()

	server.requireAuth(server.exportConfig).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/x-yaml; charset=utf-8" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	contentDisposition := response.Header().Get("Content-Disposition")
	if !strings.HasPrefix(contentDisposition, "attachment; filename=\"ddns-config-") || !strings.HasSuffix(contentDisposition, ".yaml\"") {
		t.Fatalf("Content-Disposition = %q", contentDisposition)
	}
	output := response.Body.String()
	if strings.Contains(output, "auth:") || strings.Contains(output, "current-user") || strings.Contains(output, "current-hash") {
		t.Fatalf("export leaked Web auth: %s", output)
	}
	exported, err := parseImportedConfig(strings.NewReader(output), "ddns-config.yaml")
	if err != nil {
		t.Fatalf("export cannot be imported: %v", err)
	}
	if len(exported.Providers) != 1 || exported.Providers[0].KeySecret != "exported-secret" {
		t.Fatalf("exported providers = %#v", exported.Providers)
	}
	if exported.Webhook.URL != "https://example.com/webhook" {
		t.Fatalf("exported webhook URL = %q", exported.Webhook.URL)
	}
}

func TestExportConfigRequiresAuthenticatedConsole(t *testing.T) {
	configuredServer, _ := newImportTestServer(t, `providers: []
webhook:
  url: ""
  body: ""
  headers: []
auth:
  username: current-user
  passwordHash: current-hash
`)
	configuredResponse := httptest.NewRecorder()
	configuredServer.requireAuth(configuredServer.exportConfig).ServeHTTP(
		configuredResponse,
		httptest.NewRequest(http.MethodGet, "/export", nil),
	)
	if configuredResponse.Code != http.StatusSeeOther || configuredResponse.Header().Get("Location") != "/login" {
		t.Fatalf("configured response = (%d, %q), want login redirect", configuredResponse.Code, configuredResponse.Header().Get("Location"))
	}

	setupServer, _ := newImportTestServer(t, `providers: []
webhook:
  url: ""
  body: ""
  headers: []
auth: {}
`)
	setupResponse := httptest.NewRecorder()
	setupServer.requireAuth(setupServer.exportConfig).ServeHTTP(
		setupResponse,
		httptest.NewRequest(http.MethodGet, "/export", nil),
	)
	if setupResponse.Code != http.StatusSeeOther || setupResponse.Header().Get("Location") != "/setup" {
		t.Fatalf("setup response = (%d, %q), want setup redirect", setupResponse.Code, setupResponse.Header().Get("Location"))
	}
}

const validImportYAML = `providers:
  - name: imported
    provider: aliyun
    keyId: imported-id
    keySecret: imported-secret
    forceInterval: 5
    records: []
webhook:
  url: ""
  body: ""
  headers: []
auth:
  username: imported-user
  passwordHash: imported-hash
`

func newImportTestServer(t *testing.T, configData string) (*Server, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(configData), 0600); err != nil {
		t.Fatal(err)
	}
	templates, err := parseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		configPath: configPath,
		templates:  templates,
		sessions:   newSessionStore(),
	}, configPath
}

func newImportRequest(t *testing.T, filename, configData, csrf string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if csrf != "" {
		if err := writer.WriteField("csrf", csrf); err != nil {
			t.Fatal(err)
		}
	}
	file, err := writer.CreateFormFile("configFile", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(configData)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
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
