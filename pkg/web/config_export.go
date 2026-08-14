package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"ddns/pkg/config"
	"go.yaml.in/yaml/v3"
)

type exportedConfig struct {
	Providers []config.Provider `yaml:"providers"`
	Webhook   config.Webhook    `yaml:"webhook"`
}

func (s *Server) exportConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg, err := s.readConfig()
	if err != nil {
		slog.Error("Web 配置导出失败", "stage", "read", "err", err)
		s.renderError(w, r, err)
		return
	}
	data, err := yaml.Marshal(exportedConfig{
		Providers: cfg.Providers,
		Webhook:   cfg.Webhook,
	})
	if err != nil {
		slog.Error("Web 配置导出失败", "stage", "marshal", "err", err)
		s.renderError(w, r, fmt.Errorf("生成导出配置失败: %w", err))
		return
	}
	filename := "ddns-config-" + time.Now().Format("20060102-150405") + ".yaml"
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := w.Write(data); err != nil {
		slog.Warn("Web 配置导出失败", "stage", "write", "err", err)
		return
	}
	slog.Info(
		"Web 配置导出成功",
		"providers", len(cfg.Providers),
		"webhookConfigured", cfg.Webhook.URL != "",
	)
}
