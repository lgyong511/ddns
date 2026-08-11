package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ddns/pkg/addr"
	"ddns/pkg/config"
	ddnslog "ddns/pkg/log"
	"ddns/pkg/provider"
	"ddns/pkg/version"

	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "ddns_session"
	sessionTTL    = 2 * time.Hour
)

var (
	ipv4Preset = "https://myip.ipip.net, https://ddns.oray.com/checkip, https://ip.3322.net, https://4.ipw.cn, https://v4.yinghualuo.cn/bejson"
	ipv6Preset = "https://speed.neu6.edu.cn/getIP.php, https://v6.ident.me, https://6.ipw.cn, https://v6.yinghualuo.cn/bejson"
)

type Reloader interface {
	Reload() error
}

type Server struct {
	configPath string
	reloader   Reloader
	logs       *ddnslog.Buffer
	templates  *template.Template
	sessions   *sessionStore
}

type Options struct {
	ConfigPath string
	Reloader   Reloader
	Logs       *ddnslog.Buffer
}

func New(options Options) (*Server, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	logs := options.Logs
	if logs == nil {
		logs = ddnslog.DefaultBuffer
	}
	return &Server{
		configPath: options.ConfigPath,
		reloader:   options.Reloader,
		logs:       logs,
		templates:  tmpl,
		sessions:   newSessionStore(),
	}, nil
}

func EnsureConfigFile(path string) error {
	return PrepareConfigFile(path, nil)
}

func PrepareConfigFile(path string, importCandidates []string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	for _, candidate := range importCandidates {
		if samePath(path, candidate) {
			continue
		}
		cfg, err := loadConfig(candidate)
		if err != nil {
			continue
		}
		if err := cfg.Validate(); err != nil {
			continue
		}
		return saveConfig(path, &cfg)
	}

	cfg := config.Config{Providers: []config.Provider{}, Webhook: config.Webhook{Headers: []string{}}}
	return saveConfig(path, &cfg)
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.ServeHTTP)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Path, "/")
	parts := splitPath(path)

	switch {
	case r.URL.Path == "/static/style.css":
		s.style(w, r)
	case r.URL.Path == "/static/logo.svg":
		s.logo(w, r)
	case path == "setup":
		s.setup(w, r)
	case path == "login":
		s.login(w, r)
	case path == "logout":
		s.requireAuth(s.logout)(w, r)
	case path == "password":
		s.requireAuth(s.changePassword)(w, r)
	case path == "":
		s.requireAuth(s.home)(w, r)
	case path == "logs":
		s.requireAuth(s.logsPage)(w, r)
	case path == "logs/stream":
		s.requireAuth(s.logsStream)(w, r)
	case path == "api/nics":
		s.requireAuth(s.apiNics)(w, r)
	case path == "providers/new":
		s.requireAuth(s.providerForm(-1))(w, r)
	case path == "providers" && r.Method == http.MethodPost:
		s.requireAuth(s.saveProvider(-1))(w, r)
	case len(parts) == 3 && parts[0] == "providers" && parts[2] == "edit":
		s.withIndex(w, r, parts[1], func(idx int) { s.requireAuth(s.providerForm(idx))(w, r) })
	case len(parts) == 2 && parts[0] == "providers" && r.Method == http.MethodPost:
		s.withIndex(w, r, parts[1], func(idx int) { s.requireAuth(s.saveProvider(idx))(w, r) })
	case len(parts) == 3 && parts[0] == "providers" && parts[2] == "delete" && r.Method == http.MethodPost:
		s.withIndex(w, r, parts[1], func(idx int) { s.requireAuth(s.deleteProvider(idx))(w, r) })
	case len(parts) == 4 && parts[0] == "providers" && parts[2] == "records" && parts[3] == "new":
		s.withIndex(w, r, parts[1], func(pIdx int) { s.requireAuth(s.recordForm(pIdx, -1))(w, r) })
	case len(parts) == 3 && parts[0] == "providers" && parts[2] == "records" && r.Method == http.MethodPost:
		s.withIndex(w, r, parts[1], func(pIdx int) { s.requireAuth(s.saveRecord(pIdx, -1))(w, r) })
	case len(parts) == 5 && parts[0] == "providers" && parts[2] == "records" && parts[4] == "edit":
		s.withTwoIndexes(w, r, parts[1], parts[3], func(pIdx, rIdx int) { s.requireAuth(s.recordForm(pIdx, rIdx))(w, r) })
	case len(parts) == 4 && parts[0] == "providers" && parts[2] == "records" && r.Method == http.MethodPost:
		s.withTwoIndexes(w, r, parts[1], parts[3], func(pIdx, rIdx int) { s.requireAuth(s.saveRecord(pIdx, rIdx))(w, r) })
	case len(parts) == 5 && parts[0] == "providers" && parts[2] == "records" && parts[4] == "delete" && r.Method == http.MethodPost:
		s.withTwoIndexes(w, r, parts[1], parts[3], func(pIdx, rIdx int) { s.requireAuth(s.deleteRecord(pIdx, rIdx))(w, r) })
	case path == "webhook":
		s.requireAuth(s.webhook)(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	cfg, _ := loadConfig(s.configPath)
	if cfg.Auth.PasswordHash != "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		s.render(w, "setup.html", map[string]any{"Title": "首次设置"})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	confirm := r.FormValue("confirm")
	if username == "" || password == "" || password != confirm {
		s.render(w, "setup.html", map[string]any{"Title": "首次设置", "Error": "账号不能为空，且两次密码必须一致"})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "无法生成密码哈希", http.StatusInternalServerError)
		return
	}
	cfg.Auth = config.Auth{Username: username, PasswordHash: string(hash)}
	if err := s.persist(&cfg); err != nil {
		s.render(w, "setup.html", map[string]any{"Title": "首次设置", "Error": err.Error()})
		return
	}
	slog.Info("Web 登录账号已初始化", "username", username)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	cfg, _ := loadConfig(s.configPath)
	if cfg.Auth.PasswordHash == "" {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		s.render(w, "login.html", map[string]any{"Title": "登录"})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(username), []byte(cfg.Auth.Username)) != 1 ||
		bcrypt.CompareHashAndPassword([]byte(cfg.Auth.PasswordHash), []byte(password)) != nil {
		s.render(w, "login.html", map[string]any{"Title": "登录", "Error": "账号或密码错误"})
		return
	}
	token, csrf, err := s.sessions.create()
	if err != nil {
		http.Error(w, "无法创建会话", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
	_ = csrf
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.validCSRF(r) {
		http.Error(w, "CSRF token invalid", http.StatusForbidden)
		return
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		s.renderError(w, r, err)
		return
	}
	if r.Method == http.MethodGet {
		s.render(w, "password_form.html", s.page(r, "修改密码", nil))
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

	oldPassword := r.FormValue("oldPassword")
	newPassword := r.FormValue("newPassword")
	confirm := r.FormValue("confirm")
	if bcrypt.CompareHashAndPassword([]byte(cfg.Auth.PasswordHash), []byte(oldPassword)) != nil {
		s.render(w, "password_form.html", s.page(r, "修改密码", map[string]any{"Error": "旧密码不正确"}))
		return
	}
	if newPassword == "" || newPassword != confirm {
		s.render(w, "password_form.html", s.page(r, "修改密码", map[string]any{"Error": "新密码不能为空，且两次输入必须一致"}))
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "无法生成密码哈希", http.StatusInternalServerError)
		return
	}
	cfg.Auth.PasswordHash = string(hash)
	if err := s.persist(&cfg); err != nil {
		s.render(w, "password_form.html", s.page(r, "修改密码", map[string]any{"Error": err.Error()}))
		return
	}
	s.sessions.clear()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	slog.Info("Web 登录密码已修改", "username", cfg.Auth.Username)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		s.render(w, "error.html", s.page(r, "配置管理", map[string]any{"Error": err.Error()}))
		return
	}
	s.render(w, "home.html", s.page(r, "配置管理", map[string]any{"Config": cfg}))
}

func (s *Server) providerForm(idx int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := loadConfig(s.configPath)
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		form := providerForm{Name: "", Provider: "", ForceInterval: "", Records: []recordForm{{IPVersion: "4", GetType: "url"}}}
		title := "创建DDNS配置"
		action := "/providers"
		if idx >= 0 {
			if idx >= len(cfg.Providers) {
				http.NotFound(w, r)
				return
			}
			p := cfg.Providers[idx]
			form = providerForm{Name: p.Name, Provider: p.Provider, KeyID: p.KeyID, ForceInterval: fmt.Sprint(int64(p.ForceInterval)), Records: recordForms(p.Records)}
			title = "编辑服务商"
			action = fmt.Sprintf("/providers/%d", idx)
		}
		nics, _ := nicOptions()
		s.render(w, "provider_form.html", s.page(r, title, map[string]any{"Form": form, "Action": action, "IsEdit": idx >= 0, "NICs": nics}))
	}
}

func (s *Server) saveProvider(idx int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.validCSRF(r) {
			http.Error(w, "CSRF token invalid", http.StatusForbidden)
			return
		}
		cfg, err := loadConfig(s.configPath)
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		p, err := parseProvider(r)
		if err != nil {
			s.renderProviderError(w, r, idx, err)
			return
		}
		records, err := parseProviderRecords(r)
		if err != nil {
			s.renderProviderError(w, r, idx, err)
			return
		}
		p.Records = records
		if idx >= 0 {
			if idx >= len(cfg.Providers) {
				http.NotFound(w, r)
				return
			}
			old := cfg.Providers[idx]
			if p.KeySecret == "" {
				p.KeySecret = old.KeySecret
			}
			cfg.Providers[idx] = p
		} else {
			if p.KeySecret == "" {
				s.renderProviderError(w, r, idx, fmt.Errorf("Access Key Secret 不能为空"))
				return
			}
			cfg.Providers = append(cfg.Providers, p)
		}
		if err := s.persist(&cfg); err != nil {
			s.renderProviderError(w, r, idx, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (s *Server) deleteProvider(idx int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.validCSRF(r) {
			http.Error(w, "CSRF token invalid", http.StatusForbidden)
			return
		}
		cfg, err := loadConfig(s.configPath)
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		if idx < 0 || idx >= len(cfg.Providers) {
			http.NotFound(w, r)
			return
		}
		cfg.Providers = append(cfg.Providers[:idx], cfg.Providers[idx+1:]...)
		if err := s.persist(&cfg); err != nil {
			s.renderError(w, r, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (s *Server) recordForm(pIdx, rIdx int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := loadConfig(s.configPath)
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		if pIdx < 0 || pIdx >= len(cfg.Providers) {
			http.NotFound(w, r)
			return
		}
		form := recordForm{IPVersion: "4", TTL: "", Interval: "", GetType: "url"}
		title := "新增解析记录"
		action := fmt.Sprintf("/providers/%d/records", pIdx)
		if rIdx >= 0 {
			if rIdx >= len(cfg.Providers[pIdx].Records) {
				http.NotFound(w, r)
				return
			}
			rec := cfg.Providers[pIdx].Records[rIdx]
			form = recordForm{
				Name: rec.Name, SubDomains: strings.Join(rec.SubDomains, ", "), IPVersion: fmt.Sprint(rec.IPVersion),
				TTL: fmt.Sprint(rec.TTL), Interval: fmt.Sprint(int64(rec.Interval)), GetType: rec.GetType,
				GetValue: rec.GetValue, Rule: rec.Rule,
			}
			title = "编辑解析记录"
			action = fmt.Sprintf("/providers/%d/records/%d", pIdx, rIdx)
		}
		nics, _ := nicOptions()
		s.render(w, "record_form.html", s.page(r, title, map[string]any{"Form": form, "Action": action, "NICs": nics}))
	}
}

func (s *Server) saveRecord(pIdx, rIdx int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.validCSRF(r) {
			http.Error(w, "CSRF token invalid", http.StatusForbidden)
			return
		}
		cfg, err := loadConfig(s.configPath)
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		if pIdx < 0 || pIdx >= len(cfg.Providers) {
			http.NotFound(w, r)
			return
		}
		rec, err := parseRecord(r)
		if err != nil {
			s.renderRecordError(w, r, pIdx, rIdx, err)
			return
		}
		if rIdx >= 0 {
			if rIdx >= len(cfg.Providers[pIdx].Records) {
				http.NotFound(w, r)
				return
			}
			cfg.Providers[pIdx].Records[rIdx] = rec
		} else {
			cfg.Providers[pIdx].Records = append(cfg.Providers[pIdx].Records, rec)
		}
		if err := s.persist(&cfg); err != nil {
			s.renderRecordError(w, r, pIdx, rIdx, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (s *Server) deleteRecord(pIdx, rIdx int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.validCSRF(r) {
			http.Error(w, "CSRF token invalid", http.StatusForbidden)
			return
		}
		cfg, err := loadConfig(s.configPath)
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		if pIdx < 0 || pIdx >= len(cfg.Providers) || rIdx < 0 || rIdx >= len(cfg.Providers[pIdx].Records) {
			http.NotFound(w, r)
			return
		}
		records := cfg.Providers[pIdx].Records
		cfg.Providers[pIdx].Records = append(records[:rIdx], records[rIdx+1:]...)
		if err := s.persist(&cfg); err != nil {
			s.renderError(w, r, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	cfg, err := loadConfig(s.configPath)
	if err != nil {
		s.renderError(w, r, err)
		return
	}
	if r.Method == http.MethodGet {
		form := newWebhookForm(cfg.Webhook)
		s.render(w, "webhook_form.html", s.page(r, "Webhook", map[string]any{"Form": form}))
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
	urlValue := strings.TrimSpace(r.FormValue("url"))
	if cfg.Webhook.URL != "" && urlValue == maskWebhook(cfg.Webhook.URL) {
		urlValue = cfg.Webhook.URL
	}
	cfg.Webhook = config.Webhook{URL: urlValue, Body: strings.TrimSpace(r.FormValue("body")), Headers: splitLines(r.FormValue("headers"))}
	if err := s.persist(&cfg); err != nil {
		s.render(w, "webhook_form.html", s.page(r, "Webhook", map[string]any{"Form": newWebhookForm(cfg.Webhook), "Error": err.Error()}))
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logsPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "logs.html", s.page(r, "系统日志", map[string]any{"Logs": s.logs.Snapshot()}))
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
	ch, unsubscribe := s.logs.Subscribe()
	defer unsubscribe()
	for {
		select {
		case line := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", strings.ReplaceAll(line, "\n", " "))
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) apiNics(w http.ResponseWriter, r *http.Request) {
	nics, err := nicOptions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(nics)
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, _ := loadConfig(s.configPath)
		if cfg.Auth.PasswordHash == "" {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || !s.sessions.touch(cookie.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	csrf, ok := s.sessions.csrf(cookie.Value)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(csrf), []byte(r.FormValue("csrf"))) == 1
}

func (s *Server) page(r *http.Request, title string, data map[string]any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	data["Title"] = title
	data["Version"] = version.Version
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if csrf, ok := s.sessions.csrf(cookie.Value); ok {
			data["CSRF"] = csrf
		}
	}
	return data
}

func (s *Server) persist(cfg *config.Config) error {
	if cfg.Providers == nil {
		cfg.Providers = []config.Provider{}
	}
	if cfg.Webhook.Headers == nil {
		cfg.Webhook.Headers = []string{}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := saveConfig(s.configPath, cfg); err != nil {
		return err
	}
	if s.reloader != nil {
		if err := s.reloader.Reload(); err != nil {
			return err
		}
	}
	slog.Info("配置已通过 Web 控制台保存")
	return nil
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("模板渲染失败", "template", name, "err", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, err error) {
	s.render(w, "error.html", s.page(r, "出错了", map[string]any{"Error": err.Error()}))
}

func (s *Server) renderProviderError(w http.ResponseWriter, r *http.Request, idx int, err error) {
	form := providerForm{Name: r.FormValue("name"), Provider: r.FormValue("provider"), KeyID: r.FormValue("keyId"), ForceInterval: r.FormValue("forceInterval"), Records: []recordForm{{IPVersion: "4", GetType: "url"}}}
	action := "/providers"
	if idx >= 0 {
		action = fmt.Sprintf("/providers/%d", idx)
	}
	s.render(w, "provider_form.html", s.page(r, "服务商", map[string]any{"Form": form, "Action": action, "IsEdit": idx >= 0, "Error": err.Error()}))
}

func (s *Server) renderRecordError(w http.ResponseWriter, r *http.Request, pIdx, rIdx int, err error) {
	form := recordForm{Name: r.FormValue("name"), SubDomains: r.FormValue("subDomains"), IPVersion: r.FormValue("ipVersion"), TTL: r.FormValue("ttl"), Interval: r.FormValue("interval"), GetType: r.FormValue("getType"), GetValue: r.FormValue("getValue"), Rule: r.FormValue("rule")}
	action := fmt.Sprintf("/providers/%d/records", pIdx)
	if rIdx >= 0 {
		action = fmt.Sprintf("/providers/%d/records/%d", pIdx, rIdx)
	}
	nics, _ := nicOptions()
	s.render(w, "record_form.html", s.page(r, "解析记录", map[string]any{"Form": form, "Action": action, "NICs": nics, "Error": err.Error()}))
}

func loadConfig(path string) (config.Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config.Config{Providers: []config.Provider{}, Webhook: config.Webhook{Headers: []string{}}}, nil
	}
	if err != nil {
		return config.Config{}, err
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return config.Config{}, err
	}
	if cfg.Providers == nil {
		cfg.Providers = []config.Provider{}
	}
	if cfg.Webhook.Headers == nil {
		cfg.Webhook.Headers = []string{}
	}
	return cfg, nil
}

func saveConfig(path string, cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".conf-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func samePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return absA == absB
}

type providerForm struct {
	Name          string
	Provider      string
	KeyID         string
	ForceInterval string
	Records       []recordForm
}

func recordForms(records []config.Record) []recordForm {
	forms := make([]recordForm, 0, len(records))
	for _, rec := range records {
		forms = append(forms, recordForm{Name: rec.Name, SubDomains: strings.Join(rec.SubDomains, ", "), IPVersion: fmt.Sprint(rec.IPVersion), TTL: fmt.Sprint(rec.TTL), Interval: fmt.Sprint(int64(rec.Interval)), GetType: rec.GetType, GetValue: rec.GetValue, Rule: rec.Rule})
	}
	if len(forms) == 0 {
		return []recordForm{{IPVersion: "4", GetType: "url"}}
	}
	return forms
}

func parseProviderRecords(r *http.Request) ([]config.Record, error) {
	names := r.Form["recordName"]
	if len(names) == 0 {
		return nil, nil
	}
	records := make([]config.Record, 0, len(names))
	for i := range names {
		getType := r.FormValue(fmt.Sprintf("recordGetType%d", i))
		form := recordForm{Name: names[i], SubDomains: r.Form["recordSubDomains"][i], IPVersion: r.Form["recordIPVersion"][i], TTL: r.Form["recordTTL"][i], Interval: r.Form["recordInterval"][i], GetType: getType, GetValue: r.Form["recordGetValue"][i], Rule: r.Form["recordRule"][i]}
		if form.GetType == "url" && strings.TrimSpace(form.GetValue) == "" {
			if form.IPVersion == "6" {
				form.GetValue = ipv6Preset
			} else {
				form.GetValue = ipv4Preset
			}
		}
		rec, err := parseRecordForm(form)
		if err != nil {
			return nil, fmt.Errorf("第 %d 条解析记录：%w", i+1, err)
		}
		records = append(records, rec)
	}
	return records, nil
}

func parseProvider(r *http.Request) (config.Provider, error) {
	forceInterval := parseDurationDefault(r.FormValue("forceInterval"), 5)
	p := config.Provider{
		Name: strings.TrimSpace(r.FormValue("name")), Provider: strings.TrimSpace(r.FormValue("provider")),
		KeyID: strings.TrimSpace(r.FormValue("keyId")), KeySecret: strings.TrimSpace(r.FormValue("keySecret")),
		ForceInterval: forceInterval, Records: []config.Record{},
	}
	if p.Name == "" {
		return p, fmt.Errorf("服务商名称不能为空")
	}
	if p.Provider == "" {
		return p, fmt.Errorf("请选择服务商类型")
	}
	if p.KeyID == "" {
		return p, fmt.Errorf("Access Key ID 不能为空")
	}
	return p, nil
}

type recordForm struct {
	Name       string
	SubDomains string
	IPVersion  string
	TTL        string
	Interval   string
	GetType    string
	GetValue   string
	Rule       string
}

func parseRecord(r *http.Request) (config.Record, error) {
	form := recordForm{Name: r.FormValue("name"), SubDomains: r.FormValue("subDomains"), IPVersion: r.FormValue("ipVersion"), TTL: r.FormValue("ttl"), Interval: r.FormValue("interval"), GetType: r.FormValue("getType"), GetValue: r.FormValue("getValue"), Rule: r.FormValue("rule")}
	return parseRecordForm(form)
}

func parseRecordForm(form recordForm) (config.Record, error) {
	ipVersion := provider.Version(parseIntDefault(form.IPVersion, 4))
	ttl := int64(parseIntDefault(form.TTL, 600))
	interval := parseDurationDefault(form.Interval, 30)
	getType := strings.TrimSpace(form.GetType)
	getValue := strings.TrimSpace(form.GetValue)
	if getType == "url" && getValue == "" {
		if ipVersion == provider.IPv6 {
			getValue = ipv6Preset
		} else {
			getValue = ipv4Preset
		}
	}
	rec := config.Record{
		Name: strings.TrimSpace(form.Name), SubDomains: splitDomains(form.SubDomains),
		IPVersion: ipVersion, TTL: ttl, GetType: getType, GetValue: getValue,
		Interval: interval, Rule: strings.TrimSpace(form.Rule),
	}
	if rec.Name == "" {
		return rec, fmt.Errorf("记录名称不能为空")
	}
	if len(rec.SubDomains) == 0 {
		return rec, fmt.Errorf("子域名不能为空")
	}
	if rec.GetType == "" {
		return rec, fmt.Errorf("请选择获取方式")
	}
	if rec.GetType == "duid" && rec.IPVersion != provider.IPv6 {
		return rec, fmt.Errorf("DUID标识仅支持 IPv6")
	}
	if rec.GetType != "url" && rec.GetValue == "" {
		return rec, fmt.Errorf("%s 获取方式必须填写对应值", rec.GetType)
	}
	return rec, nil
}

type webhookForm struct {
	URL        string
	DisplayURL string
	Body       string
	Headers    string
}

func newWebhookForm(webhook config.Webhook) webhookForm {
	return webhookForm{
		URL:        webhook.URL,
		DisplayURL: maskWebhook(webhook.URL),
		Body:       webhook.Body,
		Headers:    strings.Join(webhook.Headers, "\n"),
	}
}

type nicOption struct {
	Name string   `json:"name"`
	IPs  []string `json:"ips"`
}

func nicOptions() ([]nicOption, error) {
	nicMap, err := addr.GetAllNic()
	if err != nil {
		return nil, err
	}
	options := make([]nicOption, 0, len(nicMap))
	for name, ips := range nicMap {
		option := nicOption{Name: name}
		for _, ip := range ips {
			option.IPs = append(option.IPs, ip.String())
		}
		options = append(options, option)
	}
	return options, nil
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func (s *Server) withIndex(w http.ResponseWriter, r *http.Request, raw string, fn func(int)) {
	idx, err := strconv.Atoi(raw)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	fn(idx)
}

func (s *Server) withTwoIndexes(w http.ResponseWriter, r *http.Request, first, second string, fn func(int, int)) {
	a, errA := strconv.Atoi(first)
	b, errB := strconv.Atoi(second)
	if errA != nil || errB != nil {
		http.NotFound(w, r)
		return
	}
	fn(a, b)
}

func splitDomains(raw string) []string {
	replacer := strings.NewReplacer("\n", ",", "\r", ",")
	parts := strings.Split(replacer.Replace(raw), ",")
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitLines(raw string) []string {
	lines := strings.Split(raw, "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func parseIntDefault(raw string, fallback int) int {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func parseDurationDefault(raw string, fallback int64) time.Duration {
	return time.Duration(parseIntDefault(raw, int(fallback)))
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

type session struct {
	csrf       string
	lastActive time.Time
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]session
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]session)}
}

func (s *sessionStore) create() (string, string, error) {
	token, err := randomToken()
	if err != nil {
		return "", "", err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", "", err
	}
	s.mu.Lock()
	s.sessions[token] = session{csrf: csrf, lastActive: time.Now()}
	s.mu.Unlock()
	return token, csrf, nil
}

func (s *sessionStore) touch(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	sess, ok := s.sessions[token]
	if !ok {
		return false
	}
	sess.lastActive = time.Now()
	s.sessions[token] = sess
	return true
}

func (s *sessionStore) csrf(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	sess, ok := s.sessions[token]
	return sess.csrf, ok
}

func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *sessionStore) clear() {
	s.mu.Lock()
	s.sessions = make(map[string]session)
	s.mu.Unlock()
}

func (s *sessionStore) cleanupLocked() {
	now := time.Now()
	for token, sess := range s.sessions {
		if now.Sub(sess.lastActive) > sessionTTL {
			delete(s.sessions, token)
		}
	}
}
