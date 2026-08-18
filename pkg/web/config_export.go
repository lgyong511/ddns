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
	Auth      *config.Auth      `yaml:"auth,omitempty"`
}

func (s *Server) exportConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.render(w, "config_export.html", s.page(r, "导出配置", nil))
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.validCSRF(r) {
		http.Error(w, "CSRF token invalid", http.StatusForbidden)
		return
	}
	cfg, err := s.readConfig()
	if err != nil {
		slog.Error("Web 配置导出失败", "stage", "read", "err", err)
		s.renderError(w, r, err)
		return
	}
	includeAuth := r.FormValue("includeAuth") == "on"
	exported := exportedConfig{
		Providers: cfg.Providers,
		Webhook:   cfg.Webhook,
	}
	filenamePrefix := "ddns-config-"
	if includeAuth {
		auth := cfg.Auth
		exported.Auth = &auth
		filenamePrefix = "ddns-config-with-auth-"
	}
	data, err := yaml.Marshal(exported)
	if err != nil {
		slog.Error("Web 配置导出失败", "stage", "marshal", "err", err)
		s.renderError(w, r, fmt.Errorf("生成导出配置失败: %w", err))
		return
	}
	filename := filenamePrefix + time.Now().Format("20060102-150405") + ".yaml"
	w.Header().Set("Content-Type", "application/x-yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(data); err != nil {
		slog.Warn("Web 配置导出失败", "stage", "write", "err", err)
		return
	}
	slog.Info(
		"Web 配置导出成功",
		"providers", len(cfg.Providers),
		"webhookConfigured", cfg.Webhook.URL != "",
		"authIncluded", includeAuth,
	)
}
