package config

import (
	"fmt"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

// Manager 代表配置管理器，可以添加方法来加载和验证配置
type Manager struct {
	// 配置管理包viper实例
	vp *viper.Viper
	// 加载的配置数据
	config *Config
	//读写锁
	rwMutex sync.RWMutex
	// 监听配置文件变化的回调函数
	callbacks []func(*Config)
}

// NewManager 创建一个新的配置管理器实例
func NewManager() *Manager {
	return &Manager{
		vp: viper.New(),
	}
}

// Load 从指定路径加载配置文件
func (m *Manager) Load(path string) error {
	m.vp.SetConfigFile(path)
	m.vp.SetConfigType("yaml")
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
	m.rwMutex.Lock()
	defer m.rwMutex.Unlock()
	m.config = &cfg
	return nil
}

// Get 返回加载的配置数据
func (m *Manager) Get() *Config {
	m.rwMutex.RLock()
	defer m.rwMutex.RUnlock()
	return m.config
}

// RegCallback 注册配置变化的回调函数
func (m *Manager) RegCallback(cb func(*Config)) {
	m.rwMutex.Lock()
	defer m.rwMutex.Unlock()
	m.callbacks = append(m.callbacks, cb)
}

// watchConfig 监听配置文件变化并自动重新加载
func (m *Manager) watchConfig() {
	m.vp.OnConfigChange(func(in fsnotify.Event) {
		var cfg Config
		if err := m.vp.Unmarshal(&cfg); err != nil {
			return // 解析失败不更新配置，保持原有配置继续使用
		}
		if err := cfg.Validate(); err != nil {
			return // 验证失败不更新配置，保持原有配置继续使用
		}
		m.rwMutex.Lock()
		m.config = &cfg
		m.rwMutex.Unlock()

		// 调用注册的回调函数
		for _, cb := range m.callbacks {
			cb(&cfg)
		}
	})
	m.vp.WatchConfig()
}
