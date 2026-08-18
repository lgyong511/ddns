package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ddnslog "ddns/pkg/log"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "info", line: `time=now level=INFO msg=started`, want: logLevelInfo},
		{name: "short info", line: `time=now level=INF msg=started`, want: logLevelInfo},
		{name: "lowercase info", line: `time=now LEVEL=info msg=started`, want: logLevelInfo},
		{name: "warn", line: `time=now level=WARN msg=retry`, want: logLevelWarn},
		{name: "warning", line: `time=now level="WARNING" msg=retry`, want: logLevelWarn},
		{name: "error", line: `time=now level=ERROR msg=failed`, want: logLevelError},
		{name: "short error", line: `time=now level=ERR msg=failed`, want: logLevelError},
		{name: "debug", line: `time=now level=DEBUG msg=detail`, want: logLevelDebug},
		{name: "unknown level", line: `time=now level=NOTICE msg=detail`, want: logLevelOther},
		{name: "missing level", line: `time=now msg="level=ERROR inside message"`, want: logLevelOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseLogLevel(tt.line); got != tt.want {
				t.Fatalf("parseLogLevel(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestFormatLogLinesAlternatesOnlyInfo(t *testing.T) {
	lines := formatLogLines([]string{
		"level=INFO msg=first",
		"level=WARN msg=warning",
		"level=INFO msg=second",
		"level=ERROR msg=error",
		"level=INF msg=third",
	})
	if len(lines) != 5 {
		t.Fatalf("len(lines) = %d, want 5", len(lines))
	}
	if lines[0].Alternate || !lines[2].Alternate || lines[4].Alternate {
		t.Fatalf("INFO alternation = [%v %v %v], want [false true false]", lines[0].Alternate, lines[2].Alternate, lines[4].Alternate)
	}
	if lines[1].Alternate || lines[3].Alternate {
		t.Fatal("WARN or ERROR line received INFO alternate background")
	}
}

func TestLogsPageRendersEscapedRowsAndEmptyState(t *testing.T) {
	t.Run("rows", func(t *testing.T) {
		buffer := ddnslog.NewBuffer(300)
		_, err := buffer.Write([]byte(
			"time=now level=INFO msg=first\n" +
				"time=now level=WARN msg=<script>alert(1)</script>\n" +
				"time=now level=INFO msg=second\n",
		))
		if err != nil {
			t.Fatal(err)
		}
		server := newLogsTestServer(t, buffer)
		response := httptest.NewRecorder()

		server.logsPage(response, httptest.NewRequest(http.MethodGet, "/logs", nil))

		page := response.Body.String()
		checks := []string{
			`role="log"`,
			`aria-live="polite"`,
			`class="log-line log-line-info"`,
			`class="log-line log-line-warn"`,
			`class="log-line log-line-info log-line-alt"`,
			`&lt;script&gt;alert(1)&lt;/script&gt;`,
			`id="logEmpty" hidden`,
		}
		for _, want := range checks {
			if !strings.Contains(page, want) {
				t.Fatalf("logs page does not contain %q: %s", want, page)
			}
		}
		if strings.Contains(page, "<script>alert(1)</script>") {
			t.Fatal("logs page rendered log text as HTML")
		}
	})

	t.Run("empty", func(t *testing.T) {
		server := newLogsTestServer(t, ddnslog.NewBuffer(300))
		response := httptest.NewRecorder()

		server.logsPage(response, httptest.NewRequest(http.MethodGet, "/logs", nil))

		page := response.Body.String()
		if !strings.Contains(page, "暂无日志，等待运行事件。") || strings.Contains(page, `id="logEmpty" hidden`) {
			t.Fatalf("empty logs page is incorrect: %s", page)
		}
	})
}

func TestLogsStreamSendsJSONAndStopsOnCancel(t *testing.T) {
	buffer := ddnslog.NewBuffer(300)
	server := newLogsTestServer(t, buffer)
	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodGet, "/logs/stream", nil).WithContext(ctx)
	response := newStreamingRecorder()
	done := make(chan struct{})
	go func() {
		server.logsStream(response, request)
		close(done)
	}()
	waitForStreamBody(t, response, "retry: 3000\n\n")

	if _, err := buffer.Write([]byte("time=now level=WARN msg=<unsafe>&value\n")); err != nil {
		t.Fatal(err)
	}
	body := waitForStreamBody(t, response, `"level":"warn"`)
	payload := sseData(body)
	if strings.Contains(payload, "<unsafe>") {
		t.Fatalf("SSE payload did not escape HTML-sensitive characters: %s", payload)
	}
	var line logLineView
	if err := json.Unmarshal([]byte(payload), &line); err != nil {
		t.Fatalf("unmarshal SSE payload %q: %v", payload, err)
	}
	if line.Level != logLevelWarn || line.Text != "time=now level=WARN msg=<unsafe>&value" {
		t.Fatalf("SSE line = %#v", line)
	}
	if response.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("log stream did not stop after request cancellation")
	}
}

func TestLogsPageScriptUsesSafeRowsAndResetsAlternation(t *testing.T) {
	data, err := content.ReadFile("templates/logs.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)
	checks := []string{
		"row.textContent =",
		"infoCount = 0",
		"const maxLines = 300",
		"JSON.parse(event.data)",
		"box.scrollTop = box.scrollHeight",
	}
	for _, want := range checks {
		if !strings.Contains(page, want) {
			t.Fatalf("logs script does not contain %q", want)
		}
	}

	styles, err := content.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".log-line-info.log-line-alt", ".log-line-warn", ".log-line-error"} {
		if !strings.Contains(string(styles), want) {
			t.Fatalf("log styles do not contain %q", want)
		}
	}
}

func newLogsTestServer(t *testing.T, buffer *ddnslog.Buffer) *Server {
	t.Helper()
	server, err := New(Options{Logs: buffer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Errorf("closing logs test server: %v", err)
		}
	})
	return server
}

func sseData(body string) string {
	start := strings.LastIndex(body, "data: ")
	if start < 0 {
		return ""
	}
	data := body[start+len("data: "):]
	if end := strings.IndexByte(data, '\n'); end >= 0 {
		data = data[:end]
	}
	return data
}
