package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"go.yaml.in/yaml/v3"
)

type Manager struct {
	config         *Config
	document       *yaml.Node
	fingerprint    [sha256.Size]byte
	fingerprintSet bool
	rwMutex        sync.RWMutex
	opMutex        sync.Mutex
	callbacks      []func()
	path           string
	watcher        *fsnotify.Watcher
	watchDone      chan struct{}
	watchOnce      sync.Once
	closeOnce      sync.Once
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Load(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("配置文件路径不能为空")
	}

	m.opMutex.Lock()
	callbacks, err := m.reloadLocked(path)
	if err == nil {
		m.path = path
	}
	m.opMutex.Unlock()
	if err != nil {
		return err
	}
	m.notifyCallbacks(callbacks)
	return m.startWatcher()
}

func (m *Manager) Reload() error {
	m.opMutex.Lock()
	callbacks, err := m.reloadLocked(m.path)
	m.opMutex.Unlock()
	if err != nil {
		return err
	}
	m.notifyCallbacks(callbacks)
	return nil
}

func (m *Manager) Get() (*Config, error) {
	m.rwMutex.RLock()
	defer m.rwMutex.RUnlock()
	if m.config == nil {
		return nil, errors.New("没有配置文件，请使用Load加载")
	}
	return cloneConfig(m.config), nil
}

func (m *Manager) Save(cfg *Config) error {
	if cfg == nil {
		return errors.New("配置不能为空")
	}
	next := cloneConfig(cfg)
	if err := next.Validate(); err != nil {
		return fmt.Errorf("配置验证失败: %w", err)
	}

	m.opMutex.Lock()
	callbacks, err := m.saveLocked(next)
	m.opMutex.Unlock()
	if err != nil {
		return err
	}
	m.notifyCallbacks(callbacks)
	return nil
}

func (m *Manager) RegCallback(cb func()) {
	if cb == nil {
		return
	}
	m.rwMutex.Lock()
	m.callbacks = append(m.callbacks, cb)
	m.rwMutex.Unlock()
}

func (m *Manager) Close() error {
	var err error
	m.closeOnce.Do(func() {
		if m.watcher == nil {
			return
		}
		close(m.watchDone)
		err = m.watcher.Close()
	})
	return err
}

func (m *Manager) reloadLocked(path string) ([]func(), error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("配置文件路径未设置")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	document, cfg, err := parseDocument(data)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}
	fingerprint := sha256.Sum256(data)
	m.path = path
	m.rwMutex.Lock()
	m.document = document
	m.fingerprint = fingerprint
	m.fingerprintSet = true
	callbacks := m.applyConfigLocked(&cfg)
	m.rwMutex.Unlock()
	return callbacks, nil
}

func (m *Manager) saveLocked(cfg *Config) ([]func(), error) {
	m.rwMutex.RLock()
	path := m.path
	document := cloneYAMLNode(m.document)
	oldFingerprint := m.fingerprint
	fingerprintSet := m.fingerprintSet
	m.rwMutex.RUnlock()
	if path == "" || document == nil || !fingerprintSet {
		return nil, errors.New("配置文件尚未加载")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if sha256.Sum256(data) != oldFingerprint {
		return nil, errors.New("配置文件已被外部修改，请重新加载后再保存")
	}
	desired, err := nodeFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	mergeYAMLNode(document, desired)
	data, err = yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("生成配置文件失败: %w", err)
	}
	if err := writeConfigFile(path, data); err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(data)
	m.rwMutex.Lock()
	m.document = document
	m.fingerprint = fingerprint
	m.fingerprintSet = true
	callbacks := m.applyConfigLocked(cfg)
	m.rwMutex.Unlock()
	return callbacks, nil
}

func (m *Manager) applyConfigLocked(cfg *Config) []func() {
	if m.config != nil && reflect.DeepEqual(m.config, cfg) {
		return nil
	}
	m.config = cloneConfig(cfg)
	return slices.Clone(m.callbacks)
}

func (m *Manager) notifyCallbacks(callbacks []func()) {
	for _, cb := range callbacks {
		cb()
	}
}

func (m *Manager) startWatcher() error {
	m.opMutex.Lock()
	watchPath := m.path
	m.opMutex.Unlock()
	var startErr error
	m.watchOnce.Do(func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			startErr = err
			return
		}
		m.watcher = watcher
		m.watchDone = make(chan struct{})
		if err := watcher.Add(filepath.Dir(watchPath)); err != nil {
			_ = watcher.Close()
			m.watcher = nil
			startErr = err
			return
		}
		go m.watchConfig(watcher, watchPath, m.watchDone)
	})
	if m.watcher == nil {
		if startErr != nil {
			return fmt.Errorf("无法监听配置文件目录: %w", startErr)
		}
		return errors.New("无法监听配置文件目录")
	}
	return nil
}

func (m *Manager) watchConfig(watcher *fsnotify.Watcher, path string, done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) != filepath.Clean(path) || event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if err := m.Reload(); err != nil {
				slog.Error("热加载配置文件失败", "path", path, "err", err)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			slog.Error("配置文件监听失败", "path", path, "err", err)
		}
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
