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
	callbacks []func()
}

// NewManager 创建一个新的配置管理器实例
func NewManager() *Manager {
	return &Manager{
		vp: viper.New(),
	}
}

// Load 从指定路径加载配置文件
func (m *Manager) Load(path string) error {
	//设置配置文件目录
	m.vp.SetConfigFile(path)
	//设置配置文件格式
	m.vp.SetConfigType("yaml")
	//读取配置文件
	if err := m.vp.ReadInConfig(); err != nil {
		return err
	}
	//反序化配置到结构体
	var cfg Config
	if err := m.vp.Unmarshal(&cfg); err != nil {
		return err
	}
	// 检测配置有效性
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("配置验证失败: %v", err)
	}
	//加锁
	m.rwMutex.Lock()
	//函数结束时解锁
	defer m.rwMutex.Unlock()
	//把配置文件赋值给Manager字段
	m.config = &cfg
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
	return m.config, nil
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

		// 调用回调函数
		for _, cb := range m.callbacks {
			cb()
		}
	})
	// 开启配置文件修改监听
	m.vp.WatchConfig()
}
