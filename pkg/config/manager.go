package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

// Manager 代表配置管理器，可以添加方法来加载和验证配置
type Manager struct {
	// 配置管理包viper实例
	vp *viper.Viper
	// 加载的配置数据
	config *Config
	//读写锁
	rwMutex sync.RWMutex
	// 串行化配置文件更新和变更通知
	opMutex sync.Mutex
	// 监听配置文件变化的回调函数
	callbacks []func()
	path      string
}

// NewManager 创建一个新的配置管理器实例
func NewManager() *Manager {
	return &Manager{
		vp: viper.New(),
	}
}

// Load 从指定路径加载配置文件
func (m *Manager) Load(path string) error {
	m.path = path
	//设置配置文件目录
	m.vp.SetConfigFile(path)
	//设置配置文件格式
	m.vp.SetConfigType("yaml")
	if err := m.Reload(); err != nil {
		return err
	}
	m.watchConfig()
	return nil
}

// Reload 重新读取配置文件并通知监听者。
func (m *Manager) Reload() error {
	m.opMutex.Lock()
	var callbacks []func()
	defer func() {
		m.opMutex.Unlock()
		m.notifyCallbacks(callbacks)
	}()
	if err := m.vp.ReadInConfig(); err != nil {
		return err
	}
	var cfg Config
	if err := m.vp.Unmarshal(&cfg); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("配置验证失败: %v", err)
	}
	callbacks = m.applyConfigLocked(&cfg)
	return nil
}

// Get 返回配置数据，禁止写操作
// 请确保调用Load加载配置后使用
func (m *Manager) Get() (*Config, error) {
	m.rwMutex.RLock()
	defer m.rwMutex.RUnlock()
	if m.config == nil {
		return nil, fmt.Errorf("没有配置文件，请使用Load加载！")
	}
	return cloneConfig(m.config), nil
}

// Save 校验、持久化并发布新的配置，整个更新过程由管理器串行化。
func (m *Manager) Save(cfg *Config) error {
	m.opMutex.Lock()
	var callbacks []func()
	defer func() {
		m.opMutex.Unlock()
		m.notifyCallbacks(callbacks)
	}()
	if cfg == nil {
		return fmt.Errorf("配置不能为空")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("配置验证失败: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	m.rwMutex.Lock()
	if m.path == "" {
		m.rwMutex.Unlock()
		return fmt.Errorf("配置文件路径未设置")
	}
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		m.rwMutex.Unlock()
		return err
	}
	tmp, err := os.CreateTemp(dir, ".conf-*.yaml")
	if err != nil {
		m.rwMutex.Unlock()
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		m.rwMutex.Unlock()
		return err
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		m.rwMutex.Unlock()
		return err
	}
	if err := tmp.Close(); err != nil {
		m.rwMutex.Unlock()
		return err
	}
	if err := os.Rename(tmpName, m.path); err != nil {
		m.rwMutex.Unlock()
		return err
	}
	m.rwMutex.Unlock()
	callbacks = m.applyConfigLocked(cfg)
	return nil
}

func (m *Manager) applyConfigLocked(cfg *Config) []func() {
	m.rwMutex.Lock()
	if m.config != nil && reflect.DeepEqual(m.config, cfg) {
		m.rwMutex.Unlock()
		return nil
	}
	m.config = cloneConfig(cfg)
	callbacks := slices.Clone(m.callbacks)
	m.rwMutex.Unlock()
	return callbacks
}

func (m *Manager) notifyCallbacks(callbacks []func()) {
	for _, cb := range callbacks {
		cb()
	}
}

func cloneConfig(cfg *Config) *Config {
	clone := *cfg
	clone.Providers = slices.Clone(cfg.Providers)
	for i := range clone.Providers {
		clone.Providers[i].Records = slices.Clone(cfg.Providers[i].Records)
		for j := range clone.Providers[i].Records {
			clone.Providers[i].Records[j].SubDomains = slices.Clone(cfg.Providers[i].Records[j].SubDomains)
		}
	}
	clone.Webhook.Headers = slices.Clone(cfg.Webhook.Headers)
	return &clone
}

// RegCallback 注册配置变化的回调函数
func (m *Manager) RegCallback(cb func()) {
	m.rwMutex.Lock()
	defer m.rwMutex.Unlock()
	m.callbacks = append(m.callbacks, cb)
}

// watchConfig 监听配置文件变化并自动重新加载
func (m *Manager) watchConfig() {
	m.vp.OnConfigChange(func(in fsnotify.Event) {
		m.opMutex.Lock()
		var callbacks []func()
		defer func() {
			m.opMutex.Unlock()
			m.notifyCallbacks(callbacks)
		}()
		var cfg Config
		if err := m.vp.Unmarshal(&cfg); err != nil {
			// 解析失败不更新配置，保持原有配置继续使用
			slog.Error("热加载配置文件失败！解析新配置失败！", "err", err)
			return
		}
		if err := cfg.Validate(); err != nil {
			// 验证失败不更新配置，保持原有配置继续使用
			slog.Error("热加载配置文件失败！验证新配置失败！", "err", err)
			return
		}
		callbacks = m.applyConfigLocked(&cfg)
	})
	// 开启配置文件修改监听
	m.vp.WatchConfig()
}
