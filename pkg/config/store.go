package config

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

const defaultConfigFile = "config.yaml"

func ResolvePath(explicit string, executableDir string) (string, bool, error) {
	if strings.TrimSpace(explicit) != "" {
		path, err := filepath.Abs(explicit)
		if err != nil {
			return "", true, fmt.Errorf("解析配置文件路径失败: %w", err)
		}
		return filepath.Clean(path), true, nil
	}
	if strings.TrimSpace(executableDir) == "" {
		return "", false, errors.New("程序目录不能为空")
	}
	return filepath.Join(executableDir, "config", defaultConfigFile), false, nil
}

func PrepareDefaultFile(path string, legacyPath string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(legacyPath); err == nil {
		data, err := os.ReadFile(legacyPath)
		if err != nil {
			return err
		}
		document, cfg, err := parseDocument(data)
		if err != nil {
			slog.Warn("旧配置文件无效，将创建新的默认配置", "legacy", legacyPath, "err", err)
			return createEmptyConfig(path)
		}
		if err := cfg.Validate(); err != nil {
			slog.Warn("旧配置文件校验失败，将创建新的默认配置", "legacy", legacyPath, "err", err)
			return createEmptyConfig(path)
		}
		desired, err := nodeFromConfig(&cfg)
		if err != nil {
			return err
		}
		mergeYAMLNode(document, desired)
		data, err = yaml.Marshal(document)
		if err != nil {
			return err
		}
		if err := writeConfigFile(path, data); err != nil {
			return fmt.Errorf("写入迁移配置失败: %w", err)
		}
		slog.Info("已将旧配置迁移到新默认路径", "legacy", legacyPath, "config", path)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return createEmptyConfig(path)
}

func createEmptyConfig(path string) error {
	empty := Config{Providers: []Provider{}, Webhook: Webhook{Headers: []string{}}}
	data, err := yaml.Marshal(&empty)
	if err != nil {
		return err
	}
	return writeConfigFile(path, data)
}

func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	_, cfg, err := parseDocument(data)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("配置校验失败: %w", err)
	}
	return cfg, nil
}

func Parse(r io.Reader, filename string) (Config, error) {
	if extension := strings.ToLower(filepath.Ext(filename)); extension != ".yaml" && extension != ".yml" {
		return Config{}, errors.New("仅支持 .yaml 或 .yml 配置文件")
	}
	data, err := io.ReadAll(io.LimitReader(r, 1<<20+1))
	if err != nil {
		return Config{}, fmt.Errorf("读取导入文件失败: %w", err)
	}
	if len(data) == 0 {
		return Config{}, errors.New("导入文件不能为空")
	}
	if len(data) > 1<<20 {
		return Config{}, errors.New("导入文件超过 1 MiB 限制")
	}
	_, cfg, err := parseDocument(data)
	if err != nil {
		return Config{}, fmt.Errorf("解析 YAML 配置失败: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("配置校验失败: %w", err)
	}
	return cfg, nil
}

func SaveFile(path string, cfg *Config) error {
	if cfg == nil {
		return errors.New("配置不能为空")
	}
	next := cloneConfig(cfg)
	if err := next.Validate(); err != nil {
		return fmt.Errorf("配置验证失败: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	document, _, err := parseDocument(data)
	if err != nil {
		return err
	}
	desired, err := nodeFromConfig(next)
	if err != nil {
		return err
	}
	mergeYAMLNode(document, desired)
	data, err = yaml.Marshal(document)
	if err != nil {
		return err
	}
	return writeConfigFile(path, data)
}

func parseDocument(data []byte) (*yaml.Node, Config, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, Config{}, err
	}
	var cfg Config
	if err := document.Decode(&cfg); err != nil {
		return nil, Config{}, err
	}
	if cfg.Providers == nil {
		cfg.Providers = []Provider{}
	}
	if cfg.Webhook.Headers == nil {
		cfg.Webhook.Headers = []string{}
	}
	return &document, cfg, nil
}

func nodeFromConfig(cfg *Config) (*yaml.Node, error) {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	return &document, nil
}

func mergeYAMLNode(dst, src *yaml.Node) {
	if dst == nil || src == nil {
		return
	}
	if dst.Kind != src.Kind {
		comments := [3]string{dst.HeadComment, dst.LineComment, dst.FootComment}
		*dst = *cloneYAMLNode(src)
		dst.HeadComment, dst.LineComment, dst.FootComment = comments[0], comments[1], comments[2]
		return
	}
	switch dst.Kind {
	case yaml.DocumentNode:
		if len(dst.Content) == 0 {
			dst.Content = []*yaml.Node{cloneYAMLNode(src.Content[0])}
			return
		}
		if len(src.Content) > 0 {
			mergeYAMLNode(dst.Content[0], src.Content[0])
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(src.Content); i += 2 {
			srcKey, srcValue := src.Content[i], src.Content[i+1]
			dstIndex := mappingIndex(dst, srcKey.Value)
			if dstIndex >= 0 {
				mergeYAMLNode(dst.Content[dstIndex+1], srcValue)
				continue
			}
			dst.Content = append(dst.Content, cloneYAMLNode(srcKey), cloneYAMLNode(srcValue))
		}
	case yaml.SequenceNode:
		if hasNamedItems(dst) && hasNamedItems(src) {
			mergeNamedSequence(dst, src)
			return
		}
		common := min(len(dst.Content), len(src.Content))
		for i := 0; i < common; i++ {
			mergeYAMLNode(dst.Content[i], src.Content[i])
		}
		if len(src.Content) < len(dst.Content) {
			dst.Content = dst.Content[:len(src.Content)]
			return
		}
		for _, node := range src.Content[common:] {
			dst.Content = append(dst.Content, cloneYAMLNode(node))
		}
	case yaml.ScalarNode:
		dst.Value = src.Value
		dst.Tag = src.Tag
	}
}

func hasNamedItems(node *yaml.Node) bool {
	if len(node.Content) == 0 {
		return false
	}
	for _, item := range node.Content {
		if item.Kind != yaml.MappingNode || mappingIndex(item, "name") < 0 {
			return false
		}
	}
	return true
}

func mergeNamedSequence(dst, src *yaml.Node) {
	byName := make(map[string]*yaml.Node, len(dst.Content))
	for _, item := range dst.Content {
		nameIndex := mappingIndex(item, "name")
		if nameIndex >= 0 && nameIndex+1 < len(item.Content) {
			byName[item.Content[nameIndex+1].Value] = item
		}
	}
	merged := make([]*yaml.Node, 0, len(src.Content))
	for _, desired := range src.Content {
		nameIndex := mappingIndex(desired, "name")
		if nameIndex >= 0 && nameIndex+1 < len(desired.Content) {
			if existing := byName[desired.Content[nameIndex+1].Value]; existing != nil {
				mergeYAMLNode(existing, desired)
				merged = append(merged, existing)
				continue
			}
		}
		merged = append(merged, cloneYAMLNode(desired))
	}
	dst.Content = merged
}

func mappingIndex(node *yaml.Node, key string) int {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return i
		}
	}
	return -1
}

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		clone.Content[i] = cloneYAMLNode(child)
	}
	return &clone
}

func writeConfigFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
