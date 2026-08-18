package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
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

type ConfigStore interface {
	Get() (*config.Config, error)
	Save(*config.Config) error
}

type ConfigChangeRegistrar interface {
	RegCallback(func())
}

type CloudOperator interface {
	provider.Getter
	provider.Deleter
	provider.Creator
}

type CloudOperatorFactory func(config.Provider) (CloudOperator, error)

type Server struct {
	configPath           string
	configMu             sync.Mutex
	configStateMu        sync.Mutex
	configSnapshot       config.Config
	configEvents         *configEventHub
	configHeartbeat      time.Duration
	reloader             Reloader
	configStore          ConfigStore
	logs                 *ddnslog.Buffer
	templates            *template.Template
	sessions             *sessionStore
	loginLimit           *loginLimiter
	cloudOperatorFactory CloudOperatorFactory
	cloudCleanupQueue    chan cloudCleanupJob
	cloudCleanupCtx      context.Context
	cloudCleanupCancel   context.CancelFunc
	cloudCleanupWG       sync.WaitGroup
	cloudCleanupMu       sync.Mutex
	cloudCleanupClosed   bool
	closeOnce            sync.Once
}

const maxRequestBodyBytes = 1 << 20

type Options struct {
	ConfigPath           string
	Reloader             Reloader
	ConfigStore          ConfigStore
	ConfigChanges        ConfigChangeRegistrar
	Logs                 *ddnslog.Buffer
	CloudOperatorFactory CloudOperatorFactory
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
	cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
	server := &Server{
		configPath:           options.ConfigPath,
		configEvents:         newConfigEventHub(),
		configHeartbeat:      defaultConfigHeartbeat,
		reloader:             options.Reloader,
		configStore:          options.ConfigStore,
		logs:                 logs,
		templates:            tmpl,
		sessions:             newSessionStore(),
		loginLimit:           newLoginLimiter(),
		cloudOperatorFactory: options.CloudOperatorFactory,
		cloudCleanupQueue:    make(chan cloudCleanupJob, 16),
		cloudCleanupCtx:      cleanupCtx,
		cloudCleanupCancel:   cleanupCancel,
	}
	if options.ConfigChanges != nil {
		if err := server.observeConfigChanges(options.ConfigChanges); err != nil {
			cleanupCancel()
			return nil, err
		}
	}
	server.startCloudCleanupWorker()
	return server, nil
}

type cloudCleanupJob struct {
	provider config.Provider
	record   config.Record
}

func (s *Server) startCloudCleanupWorker() {
	s.cloudCleanupWG.Add(1)
	go func() {
		defer s.cloudCleanupWG.Done()
		for {
			select {
			case <-s.cloudCleanupCtx.Done():
				return
			case job := <-s.cloudCleanupQueue:
				s.runCloudCleanup(job)
			}
		}
	}()
}

func (s *Server) runCloudCleanup(job cloudCleanupJob) {
	slog.Info("开始异步删除云端解析记录", "provider", job.provider.Name, "providerType", job.provider.Provider, "record", job.record.Name, "subDomains", job.record.SubDomains)
	if s.cloudOperatorFactory == nil {
		slog.Warn("删除解析记录失败", "provider", job.provider.Name, "record", job.record.Name, "stage", "cloud", "err", "云端删除功能未配置")
		return
	}
	operator, err := s.cloudOperatorFactory(job.provider)
	if err != nil {
		slog.Warn("删除解析记录失败", "provider", job.provider.Name, "record", job.record.Name, "stage", "cloud_init", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(s.cloudCleanupCtx, 45*time.Second)
	defer cancel()
	if err := deleteCloudRecordsWithRetry(ctx, operator, job.record, 3, time.Second); err != nil {
		slog.Warn("删除解析记录失败", "provider", job.provider.Name, "record", job.record.Name, "stage", "cloud", "attempts", 4, "err", err)
		return
	}
	slog.Info("云端解析记录删除成功", "provider", job.provider.Name, "record", job.record.Name)
}

// Close cancels the server lifecycle and waits for background cleanup to exit.
func (s *Server) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.configEvents.close()
		s.cloudCleanupMu.Lock()
		s.cloudCleanupClosed = true
		s.cloudCleanupCancel()
		s.cloudCleanupMu.Unlock()
	})
	done := make(chan struct{})
	go func() {
		s.cloudCleanupWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.ServeHTTP)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "请求体超过 1 MiB 限制", http.StatusRequestEntityTooLarge)
			return
		}
	}
	path := strings.Trim(r.URL.Path, "/")
	parts := splitPath(path)

	switch {
	case r.URL.Path == "/static/style.css":
		s.style(w, r)
	case r.URL.Path == "/static/logo.svg":
		s.logo(w, r)
	case r.URL.Path == "/static/config-events.js":
		s.configEventsScript(w, r)
	case path == "setup":
		s.setup(w, r)
	case path == "import":
		s.importConfig(w, r)
	case path == "export":
		s.requireAuth(s.exportConfig)(w, r)
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
	case path == "config/stream":
		s.requireAuth(s.configStream)(w, r)
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
	unlock := s.lockConfigForMutation(r)
	defer unlock()
	cfg, err := s.readConfig()
	if err != nil {
		s.renderConfigError(w, err)
		return
	}
	if cfg.Auth.PasswordHash != "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		s.render(w, "setup.html", map[string]any{"Title": "首次设置", "Imported": importSuccess(r)})
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
	if len(username) > config.MaxUsernameBytes || len(password) > config.MaxPasswordBytes || len(confirm) > config.MaxPasswordBytes {
		s.render(w, "setup.html", map[string]any{"Title": "首次设置", "Error": "账号最多 64 字节，密码最多 72 字节"})
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
	cfg, err := s.readConfig()
	if err != nil {
		s.renderConfigError(w, err)
		return
	}
	if cfg.Auth.PasswordHash == "" {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodGet {
		s.render(w, "login.html", map[string]any{
			"Title":         "登录",
			"Imported":      importSuccess(r),
			"ConfigChanged": configChangeNotice(r),
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	clientKey := loginClientKey(r)
	if retryAfter, ok := s.loginLimit.check(clientKey, time.Now()); ok {
		setRetryAfter(w, retryAfter)
		http.Error(w, "登录失败次数过多，请稍后重试", http.StatusTooManyRequests)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(username), []byte(cfg.Auth.Username)) != 1 ||
		bcrypt.CompareHashAndPassword([]byte(cfg.Auth.PasswordHash), []byte(password)) != nil {
		if retryAfter, locked := s.loginLimit.failure(clientKey, time.Now()); locked {
			setRetryAfter(w, retryAfter)
			http.Error(w, "登录失败次数过多，请稍后重试", http.StatusTooManyRequests)
			return
		}
		s.render(w, "login.html", map[string]any{"Title": "登录", "Error": "账号或密码错误"})
		return
	}
	s.loginLimit.success(clientKey)
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
	unlock := s.lockConfigForMutation(r)
	defer unlock()
	cfg, err := s.readConfig()
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
	if len(newPassword) > config.MaxPasswordBytes {
		s.render(w, "password_form.html", s.page(r, "修改密码", map[string]any{"Error": "新密码最多 72 字节"}))
		return
	}
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
	cfg, err := s.readConfig()
	if err != nil {
		s.render(w, "error.html", s.page(r, "配置管理", map[string]any{"Error": err.Error()}))
		return
	}
	configVersion, err := versionConfig(cfg)
	if err != nil {
		s.renderError(w, r, err)
		return
	}
	s.render(w, "home.html", s.page(r, "配置管理", map[string]any{"Config": cfg, "ConfigVersion": configVersion, "Imported": importSuccess(r)}))
}

func (s *Server) providerForm(idx int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := s.readConfig()
		if err != nil {
			s.renderConfigError(w, err)
			return
		}
		form := providerForm{Name: "", Provider: "", ForceInterval: "", Records: []recordForm{{IPVersion: "4", GetType: "url"}}}
		configVersion, err := versionConfig(cfg)
		if err != nil {
			s.renderError(w, r, err)
			return
		}
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
		s.render(w, "provider_form.html", s.page(r, title, map[string]any{"Form": form, "Action": action, "IsEdit": idx >= 0, "NICs": nics, "ConfigVersion": configVersion}))
	}
}

func (s *Server) saveProvider(idx int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		unlock := s.lockConfigForMutation(r)
		defer unlock()
		if !s.validCSRF(r) {
			http.Error(w, "CSRF token invalid", http.StatusForbidden)
			return
		}
		cfg, err := s.readConfig()
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		if idx >= 0 && !s.matchesConfigVersion(w, r, cfg) {
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
		unlock := s.lockConfigForMutation(r)
		defer unlock()
		if !s.validCSRF(r) {
			http.Error(w, "CSRF token invalid", http.StatusForbidden)
			return
		}
		cfg, err := s.readConfig()
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		if !s.matchesConfigVersion(w, r, cfg) {
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
		cfg, err := s.readConfig()
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		if pIdx < 0 || pIdx >= len(cfg.Providers) {
			http.NotFound(w, r)
			return
		}
		form := recordForm{IPVersion: "4", TTL: "", Interval: "", GetType: "url"}
		configVersion, err := versionConfig(cfg)
		if err != nil {
			s.renderError(w, r, err)
			return
		}
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
		s.render(w, "record_form.html", s.page(r, title, map[string]any{"Form": form, "Action": action, "NICs": nics, "ConfigVersion": configVersion}))
	}
}

func (s *Server) saveRecord(pIdx, rIdx int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		unlock := s.lockConfigForMutation(r)
		defer unlock()
		if !s.validCSRF(r) {
			http.Error(w, "CSRF token invalid", http.StatusForbidden)
			return
		}
		cfg, err := s.readConfig()
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		if !s.matchesConfigVersion(w, r, cfg) {
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
		unlock := s.lockConfigForMutation(r)
		defer unlock()
		if !s.validCSRF(r) {
			http.Error(w, "CSRF token invalid", http.StatusForbidden)
			return
		}
		cfg, err := s.readConfig()
		if err != nil {
			s.renderError(w, r, err)
			return
		}
		if !s.matchesConfigVersion(w, r, cfg) {
			return
		}
		if pIdx < 0 || pIdx >= len(cfg.Providers) || rIdx < 0 || rIdx >= len(cfg.Providers[pIdx].Records) {
			http.NotFound(w, r)
			return
		}
		record := cfg.Providers[pIdx].Records[rIdx]
		deleteCloud := r.FormValue("deleteCloud") == "true"
		slog.Warn("开始删除解析记录", "provider", cfg.Providers[pIdx].Name, "providerType", cfg.Providers[pIdx].Provider, "record", record.Name, "subDomains", record.SubDomains, "deleteCloud", deleteCloud)
		records := make([]config.Record, 0, len(cfg.Providers[pIdx].Records)-1)
		records = append(records, cfg.Providers[pIdx].Records[:rIdx]...)
		records = append(records, cfg.Providers[pIdx].Records[rIdx+1:]...)
		cfg.Providers[pIdx].Records = records
		if err := s.persist(&cfg); err != nil {
			slog.Warn("删除解析记录失败", "provider", cfg.Providers[pIdx].Name, "record", record.Name, "stage", "local", "err", err)
			s.renderError(w, r, err)
			return
		}

		if deleteCloud {
			job := cloudCleanupJob{provider: cfg.Providers[pIdx], record: record}
			job.record.SubDomains = append([]string(nil), record.SubDomains...)
			s.enqueueCloudCleanup(job)
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func (s *Server) enqueueCloudCleanup(job cloudCleanupJob) {
	s.cloudCleanupMu.Lock()
	defer s.cloudCleanupMu.Unlock()
	if s.cloudCleanupClosed {
		slog.Warn("云端删除任务未投递，服务正在关闭", "provider", job.provider.Name, "record", job.record.Name)
		return
	}
	select {
	case s.cloudCleanupQueue <- job:
		slog.Info("云端删除任务已投递", "provider", job.provider.Name, "record", job.record.Name)
	default:
		slog.Warn("云端删除任务队列已满，丢弃任务", "provider", job.provider.Name, "record", job.record.Name)
	}
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	unlock := s.lockConfigForMutation(r)
	defer unlock()
	cfg, err := s.readConfig()
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

func (s *Server) lockConfigForMutation(r *http.Request) func() {
	if r.Method != http.MethodPost {
		return func() {}
	}
	s.configMu.Lock()
	return s.configMu.Unlock
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
		cfg, err := s.readConfig()
		if err != nil {
			s.renderConfigError(w, err)
			return
		}
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

func versionConfig(cfg config.Config) (string, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("生成配置版本失败: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Server) matchesConfigVersion(w http.ResponseWriter, r *http.Request, cfg config.Config) bool {
	want, err := versionConfig(cfg)
	if err != nil {
		s.renderError(w, r, err)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.FormValue("configVersion")), []byte(want)) != 1 {
		http.Error(w, "配置已被其他页面修改，请刷新后重试", http.StatusConflict)
		return false
	}
	return true
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

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("模板渲染失败", "template", name, "err", err)
	}
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, err error) {
	s.render(w, "error.html", s.page(r, "出错了", map[string]any{"Error": err.Error()}))
}

func (s *Server) renderConfigError(w http.ResponseWriter, err error) {
	slog.Error("读取配置文件失败", "path", s.configPath, "err", err)
	http.Error(w, "配置文件读取失败", http.StatusInternalServerError)
}

func (s *Server) renderProviderError(w http.ResponseWriter, r *http.Request, idx int, err error) {
	form := providerForm{Name: r.FormValue("name"), Provider: r.FormValue("provider"), KeyID: r.FormValue("keyId"), ForceInterval: r.FormValue("forceInterval"), Records: []recordForm{{IPVersion: "4", GetType: "url"}}}
	action := "/providers"
	if idx >= 0 {
		action = fmt.Sprintf("/providers/%d", idx)
	}
	s.render(w, "provider_form.html", s.page(r, "服务商", map[string]any{"Form": form, "Action": action, "IsEdit": idx >= 0, "ConfigVersion": r.FormValue("configVersion"), "Error": err.Error()}))
}

func (s *Server) renderRecordError(w http.ResponseWriter, r *http.Request, pIdx, rIdx int, err error) {
	form := recordForm{Name: r.FormValue("name"), SubDomains: r.FormValue("subDomains"), IPVersion: r.FormValue("ipVersion"), TTL: r.FormValue("ttl"), Interval: r.FormValue("interval"), GetType: r.FormValue("getType"), GetValue: r.FormValue("getValue"), Rule: r.FormValue("rule")}
	action := fmt.Sprintf("/providers/%d/records", pIdx)
	if rIdx >= 0 {
		action = fmt.Sprintf("/providers/%d/records/%d", pIdx, rIdx)
	}
	nics, _ := nicOptions()
	s.render(w, "record_form.html", s.page(r, "解析记录", map[string]any{"Form": form, "Action": action, "NICs": nics, "ConfigVersion": r.FormValue("configVersion"), "Error": err.Error()}))
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
	fields := map[string][]string{
		"recordSubDomains": r.Form["recordSubDomains"],
		"recordIPVersion":  r.Form["recordIPVersion"],
		"recordTTL":        r.Form["recordTTL"],
		"recordInterval":   r.Form["recordInterval"],
		"recordGetValue":   r.Form["recordGetValue"],
		"recordRule":       r.Form["recordRule"],
	}
	for name, values := range fields {
		if len(values) < len(names) {
			return nil, fmt.Errorf("表单字段 %s 数量无效", name)
		}
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
	forceInterval := int64(parseIntDefault(r.FormValue("forceInterval"), 5))
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
	interval := int64(parseIntDefault(form.Interval, 30))
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

const (
	loginFailureThreshold = 5
	loginInitialLock      = 15 * time.Minute
	loginMaxLock          = 24 * time.Hour
	loginStateTTL         = 24 * time.Hour
	loginMaxStates        = 10000
)

type loginAttempt struct {
	failures    int
	lockLevel   int
	lockedUntil time.Time
	lastSeen    time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string]loginAttempt)}
}

func (l *loginLimiter) check(key string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)
	attempt, ok := l.attempts[key]
	if !ok || !now.Before(attempt.lockedUntil) {
		return 0, false
	}
	return attempt.lockedUntil.Sub(now), true
}

func (l *loginLimiter) failure(key string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanupLocked(now)
	attempt := l.attempts[key]
	if now.Before(attempt.lockedUntil) {
		return attempt.lockedUntil.Sub(now), true
	}
	attempt.failures++
	if attempt.failures < loginFailureThreshold {
		attempt.lastSeen = now
		l.attempts[key] = attempt
		l.trimLocked()
		return 0, false
	}
	lockDuration := loginInitialLock
	for level := 0; level < attempt.lockLevel; level++ {
		if lockDuration >= loginMaxLock/2 {
			lockDuration = loginMaxLock
			break
		}
		lockDuration *= 2
	}
	attempt.failures = 0
	attempt.lockLevel++
	attempt.lockedUntil = now.Add(lockDuration)
	attempt.lastSeen = now
	l.attempts[key] = attempt
	l.trimLocked()
	return lockDuration, true
}

func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

func (l *loginLimiter) cleanupLocked(now time.Time) {
	for key, attempt := range l.attempts {
		if now.Sub(attempt.lastSeen) > loginStateTTL && !now.Before(attempt.lockedUntil) {
			delete(l.attempts, key)
		}
	}
}

func (l *loginLimiter) trimLocked() {
	for len(l.attempts) > loginMaxStates {
		var oldestKey string
		var oldest time.Time
		for key, attempt := range l.attempts {
			if nowLocked(attempt) {
				continue
			}
			if oldestKey == "" || attempt.lastSeen.Before(oldest) {
				oldestKey = key
				oldest = attempt.lastSeen
			}
		}
		if oldestKey == "" {
			for key, attempt := range l.attempts {
				if oldestKey == "" || attempt.lastSeen.Before(oldest) {
					oldestKey = key
					oldest = attempt.lastSeen
				}
			}
		}
		delete(l.attempts, oldestKey)
	}
}

func nowLocked(attempt loginAttempt) bool {
	return !attempt.lockedUntil.IsZero() && time.Now().Before(attempt.lockedUntil)
}

func loginClientKey(r *http.Request) string {
	client := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(client); err == nil {
		return host
	}
	return client
}

func setRetryAfter(w http.ResponseWriter, duration time.Duration) {
	seconds := int(duration.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
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
