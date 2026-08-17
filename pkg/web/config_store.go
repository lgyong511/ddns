package web

import (
	"fmt"
	"io"
	"log/slog"
	"slices"

	"ddns/pkg/config"
)

func cloneConfig(cfg config.Config) config.Config {
	clone := cfg
	clone.Providers = slices.Clone(cfg.Providers)
	for i := range clone.Providers {
		clone.Providers[i].Records = slices.Clone(cfg.Providers[i].Records)
		for j := range clone.Providers[i].Records {
			clone.Providers[i].Records[j].SubDomains = slices.Clone(cfg.Providers[i].Records[j].SubDomains)
		}
	}
	clone.Webhook.Headers = slices.Clone(cfg.Webhook.Headers)
	return clone
}

func loadConfig(path string) (config.Config, error) {
	return config.LoadFile(path)
}

func parseImportedConfig(r io.Reader, filename string) (config.Config, error) {
	cfg, err := config.Parse(r, filename)
	if err != nil {
		return config.Config{}, fmt.Errorf("%w", err)
	}
	return cfg, nil
}

func saveConfig(path string, cfg *config.Config) error {
	return config.SaveFile(path, cfg)
}

func (s *Server) persist(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("配置不能为空")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if s.configStore != nil {
		if err := s.configStore.Save(cfg); err != nil {
			return err
		}
	} else {
		if err := config.SaveFile(s.configPath, cfg); err != nil {
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
	return config.LoadFile(s.configPath)
}
