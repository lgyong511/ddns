package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

const defaultConfigHeartbeat = 20 * time.Second

type configEvent string

const (
	configChangedEvent configEvent = "config-changed"
	authChangedEvent   configEvent = "auth-changed"
)

type configEventHub struct {
	mu      sync.Mutex
	clients map[chan configEvent]struct{}
	closed  bool
}

func newConfigEventHub() *configEventHub {
	return &configEventHub{clients: make(map[chan configEvent]struct{})}
}

func (h *configEventHub) subscribe() (<-chan configEvent, func()) {
	updates := make(chan configEvent, 1)
	h.mu.Lock()
	if h.closed {
		close(updates)
		h.mu.Unlock()
		return updates, func() {}
	}
	h.clients[updates] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return updates, func() {
		once.Do(func() {
			h.mu.Lock()
			if _, ok := h.clients[updates]; ok {
				delete(h.clients, updates)
				close(updates)
			}
			h.mu.Unlock()
		})
	}
}

func (h *configEventHub) publish(event configEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	for updates := range h.clients {
		publishLatest(updates, event)
	}
}

func publishLatest(updates chan configEvent, event configEvent) {
	select {
	case updates <- event:
		return
	default:
	}

	pending := configChangedEvent
	select {
	case pending = <-updates:
	default:
	}
	if pending == authChangedEvent {
		event = authChangedEvent
	}
	select {
	case updates <- event:
	default:
	}
}

func (h *configEventHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for updates := range h.clients {
		close(updates)
		delete(h.clients, updates)
	}
}

func (s *Server) observeConfigChanges(registrar ConfigChangeRegistrar) error {
	cfg, err := s.readConfig()
	if err != nil {
		return fmt.Errorf("初始化 Web 配置变更监听失败: %w", err)
	}
	s.configStateMu.Lock()
	s.configSnapshot = cloneConfig(cfg)
	s.configStateMu.Unlock()
	registrar.RegCallback(s.handleConfigChange)
	return nil
}

func (s *Server) handleConfigChange() {
	cfg, err := s.readConfig()
	if err != nil {
		slog.Error("读取热加载后的 Web 配置失败", "err", err)
		return
	}

	s.configStateMu.Lock()
	if reflect.DeepEqual(s.configSnapshot, cfg) {
		s.configStateMu.Unlock()
		return
	}
	authChanged := s.configSnapshot.Auth != cfg.Auth
	s.configSnapshot = cloneConfig(cfg)
	s.configStateMu.Unlock()

	event := configChangedEvent
	if authChanged {
		s.sessions.clear()
		event = authChangedEvent
	}
	s.configEvents.publish(event)
}

func (s *Server) configStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	updates, unsubscribe := s.configEvents.subscribe()
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err := fmt.Fprint(w, "retry: 3000\n\n"); err != nil {
		return
	}
	flusher.Flush()

	heartbeat := time.NewTicker(s.configHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case event, ok := <-updates:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: {}\n\n", event); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-s.cloudCleanupCtx.Done():
			return
		}
	}
}

func configChangeNotice(r *http.Request) bool {
	return strings.TrimSpace(r.URL.Query().Get("configChanged")) == "1"
}
