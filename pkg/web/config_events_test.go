package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ddns/pkg/config"
)

type notifyingConfigStore struct {
	mu       sync.Mutex
	cfg      config.Config
	getErr   error
	callback func()
}

type streamingRecorder struct {
	mu      sync.Mutex
	header  http.Header
	body    strings.Builder
	updated chan struct{}
}

func newStreamingRecorder() *streamingRecorder {
	return &streamingRecorder{
		header:  make(http.Header),
		updated: make(chan struct{}, 1),
	}
}

func (r *streamingRecorder) Header() http.Header {
	return r.header
}

func (r *streamingRecorder) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(data)
}

func (r *streamingRecorder) WriteHeader(int) {}

func (r *streamingRecorder) Flush() {
	select {
	case r.updated <- struct{}{}:
	default:
	}
}

func (r *streamingRecorder) bodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

func newNotifyingConfigStore(cfg config.Config) *notifyingConfigStore {
	return &notifyingConfigStore{cfg: cloneConfig(cfg)}
}

func (s *notifyingConfigStore) Get() (*config.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return nil, s.getErr
	}
	cfg := cloneConfig(s.cfg)
	return &cfg, nil
}

func (s *notifyingConfigStore) Save(cfg *config.Config) error {
	s.set(*cfg)
	return nil
}

func (s *notifyingConfigStore) RegCallback(callback func()) {
	s.mu.Lock()
	s.callback = callback
	s.mu.Unlock()
}

func (s *notifyingConfigStore) set(cfg config.Config) {
	s.mu.Lock()
	s.cfg = cloneConfig(cfg)
	callback := s.callback
	s.mu.Unlock()
	if callback != nil {
		callback()
	}
}

func (s *notifyingConfigStore) failGet(err error) {
	s.mu.Lock()
	s.getErr = err
	callback := s.callback
	s.mu.Unlock()
	if callback != nil {
		callback()
	}
}

func TestConfigEventHubCoalescesWithAuthPriority(t *testing.T) {
	hub := newConfigEventHub()
	updates, unsubscribe := hub.subscribe()
	defer unsubscribe()

	hub.publish(configChangedEvent)
	hub.publish(authChangedEvent)
	if event := <-updates; event != authChangedEvent {
		t.Fatalf("event = %q, want %q", event, authChangedEvent)
	}

	hub.publish(authChangedEvent)
	hub.publish(configChangedEvent)
	if event := <-updates; event != authChangedEvent {
		t.Fatalf("event = %q, want retained %q", event, authChangedEvent)
	}

	hub.close()
	if _, ok := <-updates; ok {
		t.Fatal("subscription remains open after hub close")
	}
	hub.close()
}

func TestConfigChangeCallbackBroadcastsActualChanges(t *testing.T) {
	cfg := config.Config{Auth: config.Auth{Username: "admin", PasswordHash: "hash"}}
	store := newNotifyingConfigStore(cfg)
	server := newConfigEventTestServer(t, store)
	updates, unsubscribe := server.configEvents.subscribe()
	defer unsubscribe()

	changed := cloneConfig(cfg)
	changed.Webhook.URL = "https://example.com/hook"
	store.set(changed)
	if event := <-updates; event != configChangedEvent {
		t.Fatalf("event = %q, want %q", event, configChangedEvent)
	}

	store.set(changed)
	select {
	case event := <-updates:
		t.Fatalf("duplicate config broadcast event %q", event)
	default:
	}

	store.failGet(errors.New("invalid config"))
	select {
	case event := <-updates:
		t.Fatalf("failed reload broadcast event %q", event)
	default:
	}
}

func TestWebConfigSaveBroadcastsToOtherPages(t *testing.T) {
	cfg := config.Config{Auth: config.Auth{Username: "admin", PasswordHash: "hash"}}
	store := newNotifyingConfigStore(cfg)
	server := newConfigEventTestServer(t, store)
	updates, unsubscribe := server.configEvents.subscribe()
	defer unsubscribe()

	changed := cloneConfig(cfg)
	changed.Webhook.URL = "https://example.com/hook"
	if err := server.persist(&changed); err != nil {
		t.Fatal(err)
	}
	if event := <-updates; event != configChangedEvent {
		t.Fatalf("event = %q, want %q", event, configChangedEvent)
	}
}

func TestConfigAuthChangeClearsSessionsAndBroadcasts(t *testing.T) {
	cfg := config.Config{Auth: config.Auth{Username: "admin", PasswordHash: "old-hash"}}
	store := newNotifyingConfigStore(cfg)
	server := newConfigEventTestServer(t, store)
	firstToken, _, err := server.sessions.create()
	if err != nil {
		t.Fatal(err)
	}
	secondToken, _, err := server.sessions.create()
	if err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe := server.configEvents.subscribe()
	defer unsubscribe()

	changed := cloneConfig(cfg)
	changed.Auth.PasswordHash = "new-hash"
	store.set(changed)

	if event := <-updates; event != authChangedEvent {
		t.Fatalf("event = %q, want %q", event, authChangedEvent)
	}
	if server.sessions.touch(firstToken) || server.sessions.touch(secondToken) {
		t.Fatal("sessions remain valid after auth config change")
	}
}

func TestConfigStreamSendsEventsAndHeartbeat(t *testing.T) {
	cfg := config.Config{Auth: config.Auth{Username: "admin", PasswordHash: "hash"}}
	store := newNotifyingConfigStore(cfg)
	server := newConfigEventTestServer(t, store)
	server.configHeartbeat = 5 * time.Millisecond
	token, _, err := server.sessions.create()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/config/stream", nil).WithContext(ctx)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	response := newStreamingRecorder()
	done := make(chan struct{})
	go func() {
		server.ServeHTTP(response, request)
		close(done)
	}()
	waitForStreamBody(t, response, "retry: 3000\n\n")
	if response.Header().Get("Content-Type") != "text/event-stream" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("stream headers = %#v", response.Header())
	}
	waitForStreamBody(t, response, ": heartbeat\n\n")

	changed := cloneConfig(cfg)
	changed.Webhook.URL = "https://example.com/hook"
	store.set(changed)
	waitForStreamBody(t, response, "event: config-changed\ndata: {}\n\n")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("config stream did not stop after request cancellation")
	}
}

func TestConfigStreamRequiresAuthAndStopsOnClose(t *testing.T) {
	cfg := config.Config{Auth: config.Auth{Username: "admin", PasswordHash: "hash"}}
	store := newNotifyingConfigStore(cfg)
	server := newConfigEventTestServer(t, store)

	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/config/stream", nil))
	if unauthorized.Code != http.StatusSeeOther || unauthorized.Header().Get("Location") != "/login" {
		t.Fatalf("unauthorized response = (%d, %q)", unauthorized.Code, unauthorized.Header().Get("Location"))
	}

	token, _, err := server.sessions.create()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/config/stream", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	response := newStreamingRecorder()
	done := make(chan struct{})
	go func() {
		server.ServeHTTP(response, request)
		close(done)
	}()
	waitForStreamBody(t, response, "retry: 3000\n\n")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("config stream did not stop after server close")
	}
}

func TestConfigEventPagesUseExpectedRefreshPolicies(t *testing.T) {
	tests := []struct {
		name string
		file string
		mode string
	}{
		{name: "home reloads", file: "templates/home.html", mode: "reload"},
		{name: "provider warns", file: "templates/provider_form.html", mode: "warn"},
		{name: "record warns", file: "templates/record_form.html", mode: "warn"},
		{name: "webhook warns", file: "templates/webhook_form.html", mode: "warn"},
		{name: "export warns", file: "templates/config_export.html", mode: "warn"},
		{name: "password warns", file: "templates/password_form.html", mode: "warn"},
		{name: "logs only handles auth", file: "templates/logs.html", mode: "auth"},
		{name: "error only handles auth", file: "templates/error.html", mode: "auth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := content.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			page := string(data)
			if !strings.Contains(page, `data-config-watch="`+tt.mode+`"`) || !strings.Contains(page, `/static/config-events.js`) {
				t.Fatalf("template %s does not use %q config watch mode", tt.file, tt.mode)
			}
		})
	}
}

func TestConfigImportWatchPolicyExcludesSetup(t *testing.T) {
	configured, _ := newImportTestServer(t, `providers: []
webhook: {}
auth:
  username: admin
  passwordHash: hash
`)
	token, _, err := configured.sessions.create()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/import", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	response := httptest.NewRecorder()
	configured.importConfig(response, request)
	if page := response.Body.String(); !strings.Contains(page, `data-config-watch="warn"`) || !strings.Contains(page, `/static/config-events.js`) {
		t.Fatalf("configured import page does not warn on changes: %s", page)
	}

	setup, _ := newImportTestServer(t, "providers: []\nwebhook: {}\nauth: {}\n")
	response = httptest.NewRecorder()
	setup.importConfig(response, httptest.NewRequest(http.MethodGet, "/import", nil))
	if page := response.Body.String(); strings.Contains(page, "data-config-watch") || strings.Contains(page, `/static/config-events.js`) {
		t.Fatalf("setup import page unexpectedly watches protected config stream: %s", page)
	}
}

func TestConfigEventsScriptAndLoginNotice(t *testing.T) {
	server, _ := newImportTestServer(t, `providers: []
webhook: {}
auth:
  username: admin
  passwordHash: hash
`)
	script := httptest.NewRecorder()
	server.ServeHTTP(script, httptest.NewRequest(http.MethodGet, "/static/config-events.js", nil))
	if script.Code != http.StatusOK || !strings.Contains(script.Body.String(), "new EventSource('/config/stream')") {
		t.Fatalf("script response = (%d, %q)", script.Code, script.Body.String())
	}
	if !strings.Contains(script.Body.String(), "window.location.reload()") || !strings.Contains(script.Body.String(), "/login?configChanged=1") {
		t.Fatalf("script does not implement refresh and auth redirect: %s", script.Body.String())
	}

	login := httptest.NewRecorder()
	server.login(login, httptest.NewRequest(http.MethodGet, "/login?configChanged=1", nil))
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), "账号配置已被修改，请使用最新账号密码登录") {
		t.Fatalf("login response = (%d, %q)", login.Code, login.Body.String())
	}
}

func newConfigEventTestServer(t *testing.T, store *notifyingConfigStore) *Server {
	t.Helper()
	server, err := New(Options{ConfigStore: store, ConfigChanges: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Errorf("closing test server: %v", err)
		}
	})
	return server
}

func waitForStreamBody(t *testing.T, response *streamingRecorder, wanted string) string {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		body := response.bodyString()
		if strings.Contains(body, wanted) {
			return body
		}
		select {
		case <-response.updated:
		case <-timer.C:
			t.Fatalf("stream body %q does not contain %q", body, wanted)
		}
	}
}
