package log

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"sync"

	"github.com/lmittmann/tint"
)

var DefaultBuffer = NewBuffer(300)

// InitLog 初始化日志配置。
func InitLog() {
	InitLogWithBuffer(DefaultBuffer)
}

func InitLogWithBuffer(buffer *Buffer) {
	stdout := tint.NewHandler(os.Stdout, &tint.Options{
		Level:      slog.LevelInfo,
		TimeFormat: "2006-01-02 15:04:05",
	})
	memory := slog.NewTextHandler(buffer, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(&multiHandler{handlers: []slog.Handler{stdout, memory}}))
}

type multiHandler struct {
	handlers []slog.Handler
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, record.Level) {
			if err := handler.Handle(ctx, record); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithAttrs(attrs))
	}
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithGroup(name))
	}
	return &multiHandler{handlers: handlers}
}

type Buffer struct {
	mu      sync.Mutex
	max     int
	lines   []string
	clients map[chan string]struct{}
	pending string
}

func NewBuffer(max int) *Buffer {
	return &Buffer{max: max, clients: make(map[chan string]struct{})}
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	text := b.pending + string(p)
	start := 0
	for i, r := range text {
		if r != '\n' {
			continue
		}
		line := text[start:i]
		b.appendLocked(line)
		start = i + 1
	}
	b.pending = text[start:]
	return len(p), nil
}

func (b *Buffer) Snapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.lines)
}

func (b *Buffer) Subscribe() (chan string, func()) {
	ch := make(chan string, 32)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		delete(b.clients, ch)
		close(ch)
		b.mu.Unlock()
	}
}

func (b *Buffer) appendLocked(line string) {
	if line == "" {
		return
	}
	b.lines = append(b.lines, line)
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
	for ch := range b.clients {
		select {
		case ch <- line:
		default:
		}
	}
}
