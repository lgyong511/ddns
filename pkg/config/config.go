package config

import (
	"ddns/pkg/provider"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Config 代表整个 YAML 文件的根结构
type Config struct {
	Providers []Provider `yaml:"providers" mapstructure:"providers"`
	Webhook   Webhook    `yaml:"webhook" mapstructure:"webhook"`
	Auth      Auth       `yaml:"auth" mapstructure:"auth"`
}

type Webhook struct {
	URL     string   `yaml:"url" mapstructure:"url"`
	Body    string   `yaml:"body" mapstructure:"body"`
	Headers []string `yaml:"headers" mapstructure:"headers"`
}

type Auth struct {
	Username     string `yaml:"username" mapstructure:"username"`
	PasswordHash string `yaml:"passwordHash" mapstructure:"passwordHash"`
}

// Provider 代表阿里等服务商配置
type Provider struct {
	//名称，唯一标识
	Name string `yaml:"name" mapstructure:"name"`
	//DNS服务商名称，aliyun，DNSpod等
	Provider string `yaml:"provider" mapstructure:"provider"`
	//DNS服务商密钥
	KeyID     string `yaml:"keyId" mapstructure:"keyId"`
	KeySecret string `yaml:"keySecret" mapstructure:"keySecret"`
	// 记录列表
	Records []Record `yaml:"records" mapstructure:"records"`
	// 强制同步时间，单位分钟
	ForceInterval time.Duration `yaml:"forceInterval" mapstructure:"forceInterval"`
}

func (p Provider) MarshalYAML() (any, error) {
	type providerYAML struct {
		Name          string   `yaml:"name"`
		Provider      string   `yaml:"provider"`
		KeyID         string   `yaml:"keyId"`
		KeySecret     string   `yaml:"keySecret"`
		Records       []Record `yaml:"records"`
		ForceInterval int64    `yaml:"forceInterval"`
	}
	return providerYAML{
		Name: p.Name, Provider: p.Provider, KeyID: p.KeyID, KeySecret: p.KeySecret,
		Records: p.Records, ForceInterval: int64(p.ForceInterval),
	}, nil
}

func (p *Provider) UnmarshalYAML(value *yaml.Node) error {
	type providerYAML struct {
		Name          string    `yaml:"name"`
		Provider      string    `yaml:"provider"`
		KeyID         string    `yaml:"keyId"`
		KeySecret     string    `yaml:"keySecret"`
		Records       []Record  `yaml:"records"`
		ForceInterval yaml.Node `yaml:"forceInterval"`
	}
	var raw providerYAML
	if err := value.Decode(&raw); err != nil {
		return err
	}
	forceInterval, err := durationFromYAML(raw.ForceInterval, 0)
	if err != nil {
		return err
	}
	*p = Provider{
		Name: raw.Name, Provider: raw.Provider, KeyID: raw.KeyID, KeySecret: raw.KeySecret,
		Records: raw.Records, ForceInterval: forceInterval,
	}
	return nil
}

// Record 代表具体解析记录的配置
type Record struct {
	//名称，唯一标识
	Name string `yaml:"name" mapstructure:"name"`
	//子域名列表
	SubDomains []string `yaml:"subDomains" mapstructure:"subDomains"`
	//IP地址版本
	IPVersion provider.Version `yaml:"ipVersion" mapstructure:"ipVersion"`
	// 生效时间，单位秒
	TTL int64 `yaml:"ttl" mapstructure:"ttl"`
	//获取IP地址的类型，如：CMD、URL
	GetType string `yaml:"getType" mapstructure:"getType"`
	//对应的值，如：ipconfig、https://ip.cn
	GetValue string `yaml:"getValue" mapstructure:"getValue"`
	//记录同步和获取IP地址的周期，单位秒
	Interval time.Duration `yaml:"interval" mapstructure:"interval"`
	//筛选IP地址的规则
	Rule string `yaml:"rule" mapstructure:"rule"`
}

func (r Record) MarshalYAML() (any, error) {
	type recordYAML struct {
		Name       string           `yaml:"name"`
		SubDomains []string         `yaml:"subDomains"`
		IPVersion  provider.Version `yaml:"ipVersion"`
		TTL        int64            `yaml:"ttl"`
		GetType    string           `yaml:"getType"`
		GetValue   string           `yaml:"getValue"`
		Interval   int64            `yaml:"interval"`
		Rule       string           `yaml:"rule"`
	}
	return recordYAML{
		Name: r.Name, SubDomains: r.SubDomains, IPVersion: r.IPVersion, TTL: r.TTL,
		GetType: r.GetType, GetValue: r.GetValue, Interval: int64(r.Interval), Rule: r.Rule,
	}, nil
}

func (r *Record) UnmarshalYAML(value *yaml.Node) error {
	type recordYAML struct {
		Name       string           `yaml:"name"`
		SubDomains []string         `yaml:"subDomains"`
		IPVersion  provider.Version `yaml:"ipVersion"`
		TTL        int64            `yaml:"ttl"`
		GetType    string           `yaml:"getType"`
		GetValue   string           `yaml:"getValue"`
		Interval   yaml.Node        `yaml:"interval"`
		Rule       string           `yaml:"rule"`
	}
	var raw recordYAML
	if err := value.Decode(&raw); err != nil {
		return err
	}
	interval, err := durationFromYAML(raw.Interval, 0)
	if err != nil {
		return err
	}
	*r = Record{
		Name: raw.Name, SubDomains: raw.SubDomains, IPVersion: raw.IPVersion, TTL: raw.TTL,
		GetType: raw.GetType, GetValue: raw.GetValue, Interval: interval, Rule: raw.Rule,
	}
	return nil
}

func durationFromYAML(node yaml.Node, fallback time.Duration) (time.Duration, error) {
	if node.Kind == 0 || strings.TrimSpace(node.Value) == "" {
		return fallback, nil
	}
	if node.Tag == "!!int" {
		value, err := strconv.ParseInt(node.Value, 10, 64)
		return time.Duration(value), err
	}
	if value, err := strconv.ParseInt(node.Value, 10, 64); err == nil {
		return time.Duration(value), nil
	}
	return time.ParseDuration(node.Value)
}

// Validate 检查配置的有效性
func (c *Config) Validate() error {
	var errs []error

	//检查Providers
	providerNames := make(map[string]bool)
	for i, p := range c.Providers {
		// 检查provider空值
		if p.Name == "" {
			errs = append(errs, fmt.Errorf("providers[%d].name 不能为空", i))
		}
		if p.KeyID == "" {
			errs = append(errs, fmt.Errorf("providers[%d].KeyID  不能为空", i))
		}
		if p.KeySecret == "" {
			errs = append(errs, fmt.Errorf("providers[%d].keySecret 不能为空", i))

		}
		if p.Provider == "" {
			errs = append(errs, fmt.Errorf("providers[%d].provider 不能为空", i))
		}

		// 检查provider是否重名
		if providerNames[p.Name] {
			errs = append(errs, fmt.Errorf("providers[%d].name 重复: %s", i, p.Name))
		}
		providerNames[p.Name] = true

		//检查Records
		recordNames := make(map[string]bool)
		for j, r := range p.Records {
			// 检查record空值
			if r.Name == "" {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].name 不能为空", p.Name, j))
			}
			if r.GetType == "" {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].getType 不能为空", p.Name, j))
			}
			if r.GetValue == "" {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].getValue 不能为空", p.Name, j))
			}
			if len(r.SubDomains) == 0 {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].subDomains 不能为空", p.Name, j))
			}
			if r.IPVersion != provider.IPv4 && r.IPVersion != provider.IPv6 {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].ipVersion 无效，请填写 4 或 6", p.Name, j))
			}

			// 检查record是否重名
			if recordNames[r.Name] {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].name 重复: %s", p.Name, j, r.Name))
			}
			recordNames[r.Name] = true
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
