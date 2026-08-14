package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
)

func (s *Server) importConfig(w http.ResponseWriter, r *http.Request) {
	unlock := s.lockConfigForMutation(r)
	defer unlock()
	current, err := s.readConfig()
	if err != nil {
		s.renderConfigError(w, err)
		return
	}
	isSetup := current.Auth.PasswordHash == ""
	if !isSetup && !s.hasSession(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		s.renderImportPage(w, r, isSetup, "")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(maxRequestBodyBytes); err != nil {
		slog.Warn("Web 配置导入失败", "stage", "form", "err", err)
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.renderImportPage(w, r, isSetup, "导入文件超过 1 MiB 限制")
			return
		}
		s.renderImportPage(w, r, isSetup, "读取导入表单失败")
		return
	}
	if !isSetup && !s.validCSRF(r) {
		http.Error(w, "CSRF token invalid", http.StatusForbidden)
		return
	}
	file, header, err := r.FormFile("configFile")
	if err != nil {
		slog.Warn("Web 配置导入失败", "stage", "file", "err", err)
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.renderImportPage(w, r, isSetup, "导入文件超过 1 MiB 限制")
			return
		}
		s.renderImportPage(w, r, isSetup, "请选择要导入的 YAML 配置文件")
		return
	}
	defer file.Close()
	imported, err := parseImportedConfig(file, header.Filename)
	if err != nil {
		slog.Warn("Web 配置导入失败", "stage", "parse", "err", err)
		s.renderImportPage(w, r, isSetup, err.Error())
		return
	}
	imported.Auth = current.Auth
	if err := s.persist(&imported); err != nil {
		slog.Error("Web 配置导入失败", "stage", "save", "err", err)
		s.renderImportPage(w, r, isSetup, fmt.Sprintf("保存导入配置失败: %v", err))
		return
	}
	slog.Info(
		"Web 配置导入成功",
		"providers", len(imported.Providers),
		"webhookConfigured", imported.Webhook.URL != "",
	)
	if isSetup {
		http.Redirect(w, r, "/setup?imported=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?imported=1", http.StatusSeeOther)
}

func (s *Server) hasSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	return err == nil && s.sessions.touch(cookie.Value)
}

func (s *Server) renderImportPage(w http.ResponseWriter, r *http.Request, isSetup bool, pageError string) {
	data := s.page(r, "导入配置", map[string]any{
		"IsSetup": isSetup,
		"Error":   pageError,
	})
	s.render(w, "config_import.html", data)
}

func importSuccess(r *http.Request) bool {
	return strings.TrimSpace(r.URL.Query().Get("imported")) == "1"
}
