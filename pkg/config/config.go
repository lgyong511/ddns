package config

import (
	"ddns/pkg/provider"
	"fmt"
	"time"
)

// Config 代表整个 YAML 文件的根结构
type Config struct {
	Providers []Provider `yaml:"providers" mapstructure:"providers"`
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

// Validate 检查配置的有效性
func (c *Config) Validate() error {
	//检查Providers
	providerNames := make(map[string]bool)
	for i, p := range c.Providers {
		// 检查provider空值
		if p.Name == "" {
			return fmt.Errorf("providers[%d].name 不能为空", i)
		}
		if p.KeyID == "" || p.KeySecret == "" {
			return fmt.Errorf("providers[%d] 的 keyId 和 keySecret 不能为空", i)
		}
		if p.Provider == "" {
			return fmt.Errorf("providers[%d].provider 不能为空", i)
		}

		// 检查provider是否重名
		if providerNames[p.Name] {
			return fmt.Errorf("providers[%d].name 重复: %s", i, p.Name)
		}
		providerNames[p.Name] = true

		//检查Records
		recordNames := make(map[string]bool)
		for j, r := range p.Records {
			// 检查record空值
			if r.Name == "" {
				return fmt.Errorf("providers[%d].records[%d].name 不能为空", i, j)
			}
			if r.GetType == "" {
				return fmt.Errorf("providers[%d].records[%d].getType 不能为空", i, j)
			}
			if r.GetValue == "" {
				return fmt.Errorf("providers[%d].records[%d].getValue 不能为空", i, j)
			}
			if len(r.SubDomains) == 0 {
				return fmt.Errorf("providers[%d].records[%d].subDomains 不能为空", i, j)
			}
			if r.IPVersion != provider.IPv4 && r.IPVersion != provider.IPv6 {
				return fmt.Errorf("providers[%d].records[%d].ipVersion 无效", i, j)
			}
			// 检查record是否重名
			if recordNames[r.Name] {
				return fmt.Errorf("providers[%d].records[%d].name 重复: %s", i, j, r.Name)
			}
			recordNames[r.Name] = true
		}
	}
	return nil
}
