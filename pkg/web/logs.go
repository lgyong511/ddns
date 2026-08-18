package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const (
	logLevelInfo  = "info"
	logLevelWarn  = "warn"
	logLevelError = "error"
	logLevelDebug = "debug"
	logLevelOther = "other"
)

type logLineView struct {
	Text      string `json:"text"`
	Level     string `json:"level"`
	Alternate bool   `json:"-"`
}

func (s *Server) logsPage(w http.ResponseWriter, r *http.Request) {
	lines := formatLogLines(s.logs.Snapshot())
	s.render(w, "logs.html", s.page(r, "系统日志", map[string]any{"Logs": lines}))
}

func (s *Server) logsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	updates, unsubscribe := s.logs.Subscribe()
	defer unsubscribe()
	if _, err := fmt.Fprint(w, "retry: 3000\n\n"); err != nil {
		return
	}
	flusher.Flush()
	for {
		select {
		case <-s.cloudCleanupCtx.Done():
			return
		case line, ok := <-updates:
			if !ok {
				return
			}
			data, err := json.Marshal(newLogLineView(line))
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func formatLogLines(lines []string) []logLineView {
	formatted := make([]logLineView, 0, len(lines))
	infoCount := 0
	for _, line := range lines {
		view := newLogLineView(line)
		if view.Level == logLevelInfo {
			view.Alternate = infoCount%2 == 1
			infoCount++
		}
		formatted = append(formatted, view)
	}
	return formatted
}

func newLogLineView(line string) logLineView {
	return logLineView{Text: line, Level: parseLogLevel(line)}
}

func parseLogLevel(line string) string {
	for field := range strings.FieldsSeq(line) {
		key, value, ok := strings.Cut(field, "=")
		if !ok || !strings.EqualFold(key, "level") {
			continue
		}
		switch strings.ToUpper(strings.Trim(value, `"`)) {
		case "INFO", "INF":
			return logLevelInfo
		case "WARN", "WARNING":
			return logLevelWarn
		case "ERROR", "ERR":
			return logLevelError
		case "DEBUG", "DBG":
			return logLevelDebug
		default:
			return logLevelOther
		}
	}
	return logLevelOther
}
