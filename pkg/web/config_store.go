package web

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"ddns/pkg/config"
	"go.yaml.in/yaml/v3"
)

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

func cloneConfig(cfg config.Config) config.Config {
	clone := cfg
	clone.Providers = make([]config.Provider, len(cfg.Providers))
	for i, providerCfg := range cfg.Providers {
		clone.Providers[i] = providerCfg
		clone.Providers[i].Records = append([]config.Record(nil), providerCfg.Records...)
		for j := range clone.Providers[i].Records {
			clone.Providers[i].Records[j].SubDomains = append([]string(nil), providerCfg.Records[j].SubDomains...)
		}
	}
	clone.Webhook.Headers = append([]string(nil), cfg.Webhook.Headers...)
	return clone
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
	if s.configStore != nil {
		if err := s.configStore.Save(cfg); err != nil {
			return err
		}
	} else {
		if err := saveConfig(s.configPath, cfg); err != nil {
			return err
		}
		if s.reloader != nil {
			if err := s.reloader.Reload(); err != nil {
				return err
			}
		}
	}
	slog.Info("配置已通过 Web 控制台保存")
	return nil
}

func (s *Server) readConfig() (config.Config, error) {
	if s.configStore != nil {
		cfg, err := s.configStore.Get()
		if err != nil {
			return config.Config{}, err
		}
		return cloneConfig(*cfg), nil
	}
	return loadConfig(s.configPath)
}
