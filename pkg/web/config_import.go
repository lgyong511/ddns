package web

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"ddns/pkg/config"
	"golang.org/x/crypto/bcrypt"
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
	includeAuth := r.FormValue("includeAuth") == "on"
	if includeAuth {
		if err := validateImportedAuth(&imported.Auth); err != nil {
			slog.Warn("Web 配置导入失败", "stage", "auth", "err", err)
			s.renderImportPage(w, r, isSetup, err.Error())
			return
		}
	} else {
		imported.Auth = current.Auth
	}
	if err := s.persist(&imported); err != nil {
		slog.Error("Web 配置导入失败", "stage", "save", "err", err)
		s.renderImportPage(w, r, isSetup, fmt.Sprintf("保存导入配置失败: %v", err))
		return
	}
	slog.Info(
		"Web 配置导入成功",
		"providers", len(imported.Providers),
		"webhookConfigured", imported.Webhook.URL != "",
		"authIncluded", includeAuth,
	)
	if includeAuth {
		s.sessions.clear()
		http.SetCookie(w, expiredSessionCookie())
		http.Redirect(w, r, "/login?imported=1", http.StatusSeeOther)
		return
	}
	if isSetup {
		http.Redirect(w, r, "/setup?imported=1", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?imported=1", http.StatusSeeOther)
}

func validateImportedAuth(auth *config.Auth) error {
	if auth == nil {
		return fmt.Errorf("导入文件缺少 Web 账号密码配置")
	}
	auth.Username = strings.TrimSpace(auth.Username)
	if auth.Username == "" {
		return fmt.Errorf("导入文件中的 Web 账号不能为空")
	}
	if auth.PasswordHash == "" {
		return fmt.Errorf("导入文件中的 Web 密码哈希不能为空")
	}
	if _, err := bcrypt.Cost([]byte(auth.PasswordHash)); err != nil {
		return fmt.Errorf("导入文件中的 Web 密码哈希不是有效的 bcrypt 格式")
	}
	return nil
}

func expiredSessionCookie() *http.Cookie {
	return &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode}
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
