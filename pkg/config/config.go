package config

import (
	"ddns/pkg/provider"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
	"golang.org/x/net/idna"
)

const (
	MaxProviderNameBytes   = 64
	MaxProviderTypeBytes   = 32
	MaxRecordNameBytes     = 64
	MaxAccessKeyBytes      = 256
	MaxURLBytes            = 2048
	MaxCommandBytes        = 4096
	MaxNICBytes            = 256
	MaxDUIDBytes           = 128
	MaxRuleBytes           = 512
	MaxGetTypeBytes        = 16
	MaxDomainBytes         = 253
	MaxDomainLabelBytes    = 63
	MaxWebhookURLBytes     = 2048
	MaxWebhookBodyBytes    = 64 * 1024
	MaxWebhookHeaderBytes  = 1024
	MaxWebhookHeadersBytes = 8 * 1024
	MaxUsernameBytes       = 64
	MaxPasswordBytes       = 72
	MaxPasswordHashBytes   = 128
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
	ForceInterval int64 `yaml:"forceInterval" mapstructure:"forceInterval"`
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
		Name          string   `yaml:"name"`
		Provider      string   `yaml:"provider"`
		KeyID         string   `yaml:"keyId"`
		KeySecret     string   `yaml:"keySecret"`
		Records       []Record `yaml:"records"`
		ForceInterval int64    `yaml:"forceInterval"`
	}
	var raw providerYAML
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*p = Provider{
		Name: raw.Name, Provider: raw.Provider, KeyID: raw.KeyID, KeySecret: raw.KeySecret,
		Records: raw.Records, ForceInterval: raw.ForceInterval,
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
	Interval int64 `yaml:"interval" mapstructure:"interval"`
	//筛选IP地址的规则
	Rule string `yaml:"rule" mapstructure:"rule"`
}

func (r *Record) UnmarshalYAML(value *yaml.Node) error {
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
	var raw recordYAML
	if err := value.Decode(&raw); err != nil {
		return err
	}
	*r = Record{
		Name: raw.Name, SubDomains: raw.SubDomains, IPVersion: raw.IPVersion, TTL: raw.TTL,
		GetType: raw.GetType, GetValue: raw.GetValue, Interval: raw.Interval, Rule: raw.Rule,
	}
	return nil
}

// Validate 检查配置的有效性
func (c *Config) Validate() error {
	var errs []error
	if err := c.NormalizeDomains(); err != nil {
		errs = append(errs, err)
	}

	//检查Providers
	providerNames := make(map[string]bool)
	for i, p := range c.Providers {
		// 检查provider空值
		if p.Name == "" {
			errs = append(errs, fmt.Errorf("providers[%d].name 不能为空", i))
		}
		if err := validateByteLength("providers["+strconv.Itoa(i)+"].name", p.Name, MaxProviderNameBytes); err != nil {
			errs = append(errs, err)
		}
		if p.KeyID == "" {
			errs = append(errs, fmt.Errorf("providers[%d].KeyID  不能为空", i))
		}
		if err := validateByteLength("providers["+strconv.Itoa(i)+"].keyId", p.KeyID, MaxAccessKeyBytes); err != nil {
			errs = append(errs, err)
		}
		if err := validateByteLength("providers["+strconv.Itoa(i)+"].keySecret", p.KeySecret, MaxAccessKeyBytes); err != nil {
			errs = append(errs, err)
		}
		if p.KeySecret == "" {
			errs = append(errs, fmt.Errorf("providers[%d].keySecret 不能为空", i))

		}
		if p.Provider == "" {
			errs = append(errs, fmt.Errorf("providers[%d].provider 不能为空", i))
		}
		if !validProviderTypes[p.Provider] {
			errs = append(errs, fmt.Errorf("providers[%d].provider 无效，请填写 aliyun、baidu、dnsla、tencent、huawei 或 volcengine", i))
		}
		if err := validateByteLength("providers["+strconv.Itoa(i)+"].provider", p.Provider, MaxProviderTypeBytes); err != nil {
			errs = append(errs, err)
		}
		if p.ForceInterval != 0 && (p.ForceInterval < 5 || p.ForceInterval > 30) {
			errs = append(errs, fmt.Errorf("providers[%s].forceInterval 无效，请填写 5-30 分钟", p.Name))
		}

		// 检查provider是否重名
		if providerNames[p.Name] {
			errs = append(errs, fmt.Errorf("providers[%d].name 重复: %s", i, p.Name))
		}
		providerNames[p.Name] = true

		//检查Records
		recordNames := make(map[string]bool)
		domainVersions := make(map[string]bool)
		for j, r := range p.Records {
			// 检查record空值
			if r.Name == "" {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].name 不能为空", p.Name, j))
			}
			if err := validateByteLength("providers["+p.Name+"].records["+strconv.Itoa(j)+"].name", r.Name, MaxRecordNameBytes); err != nil {
				errs = append(errs, err)
			}
			if r.GetType == "" {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].getType 不能为空", p.Name, j))
			}
			if !validGetTypes[r.GetType] {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].getType 无效，请填写 cmd、url、nic 或 duid", p.Name, j))
			}
			if err := validateByteLength("providers["+p.Name+"].records["+strconv.Itoa(j)+"].getType", r.GetType, MaxGetTypeBytes); err != nil {
				errs = append(errs, err)
			}
			if r.GetValue == "" {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].getValue 不能为空", p.Name, j))
			}
			if len(r.SubDomains) == 0 {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].subDomains 不能为空", p.Name, j))
			}
			for _, subDomain := range r.SubDomains {
				key, err := domainVersionKey(subDomain, r.IPVersion)
				if err != nil {
					continue
				}
				if domainVersions[key] {
					errs = append(errs, fmt.Errorf("providers[%s].records[%d].subDomains 与同服务商其他记录重复: %s (IPv%d)", p.Name, j, subDomain, r.IPVersion))
				}
				domainVersions[key] = true
			}
			if err := validateByteLength("providers["+p.Name+"].records["+strconv.Itoa(j)+"].getValue", r.GetValue, maxGetValueBytes(r.GetType)); err != nil {
				errs = append(errs, err)
			}
			if err := validateByteLength("providers["+p.Name+"].records["+strconv.Itoa(j)+"].rule", r.Rule, MaxRuleBytes); err != nil {
				errs = append(errs, err)
			}
			if r.IPVersion != provider.IPv4 && r.IPVersion != provider.IPv6 {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].ipVersion 无效，请填写 4 或 6", p.Name, j))
			}
			if r.TTL != 0 && (r.TTL < 1 || r.TTL > 86400) {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].ttl 无效，请填写 1-86400 秒", p.Name, j))
			}
			if r.Interval != 0 && (r.Interval < 10 || r.Interval > 60) {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].interval 无效，请填写 10-60 秒", p.Name, j))
			}
			if r.GetType == "duid" && r.IPVersion != provider.IPv6 {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].duid 仅支持 IPv6", p.Name, j))
			}

			// 检查record是否重名
			if recordNames[r.Name] {
				errs = append(errs, fmt.Errorf("providers[%s].records[%d].name 重复: %s", p.Name, j, r.Name))
			}
			recordNames[r.Name] = true
		}
	}
	if err := validateByteLength("webhook.url", c.Webhook.URL, MaxWebhookURLBytes); err != nil {
		errs = append(errs, err)
	}
	if err := validateByteLength("webhook.body", c.Webhook.Body, MaxWebhookBodyBytes); err != nil {
		errs = append(errs, err)
	}
	totalHeaderBytes := 0
	for i, header := range c.Webhook.Headers {
		if err := validateByteLength("webhook.headers["+strconv.Itoa(i)+"]", header, MaxWebhookHeaderBytes); err != nil {
			errs = append(errs, err)
		}
		totalHeaderBytes += len(header)
	}
	if totalHeaderBytes > MaxWebhookHeadersBytes {
		errs = append(errs, fmt.Errorf("webhook.headers 总长度不能超过 %d 字节", MaxWebhookHeadersBytes))
	}
	if err := validateByteLength("auth.username", c.Auth.Username, MaxUsernameBytes); err != nil {
		errs = append(errs, err)
	}
	if err := validateByteLength("auth.passwordHash", c.Auth.PasswordHash, MaxPasswordHashBytes); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// NormalizeDomains converts record domains to lowercase ASCII without a trailing dot.
func (c *Config) NormalizeDomains() error {
	var errs []error
	for i := range c.Providers {
		for j := range c.Providers[i].Records {
			record := &c.Providers[i].Records[j]
			for k, subDomain := range record.SubDomains {
				normalized, err := normalizedDomainName(subDomain)
				if err != nil {
					errs = append(errs, fmt.Errorf("providers[%s].records[%d].subDomains: %w", c.Providers[i].Name, j, err))
					continue
				}
				record.SubDomains[k] = normalized
			}
		}
	}
	return errors.Join(errs...)
}

var validProviderTypes = map[string]bool{
	"aliyun":     true,
	"baidu":      true,
	"dnsla":      true,
	"tencent":    true,
	"huawei":     true,
	"volcengine": true,
}

var validGetTypes = map[string]bool{
	"cmd":  true,
	"url":  true,
	"nic":  true,
	"duid": true,
}

func validateByteLength(field, value string, max int) error {
	if len(value) > max {
		return fmt.Errorf("%s 长度不能超过 %d 字节", field, max)
	}
	return nil
}

func maxGetValueBytes(getType string) int {
	switch getType {
	case "url":
		return MaxURLBytes
	case "cmd":
		return MaxCommandBytes
	case "nic":
		return MaxNICBytes
	case "duid":
		return MaxDUIDBytes
	default:
		return MaxCommandBytes
	}
}

func validateDomainName(value string) error {
	_, err := normalizedDomainName(value)
	return err
}

func domainVersionKey(domain string, version provider.Version) (string, error) {
	normalized, err := normalizedDomainName(domain)
	if err != nil {
		return "", err
	}
	return normalized + "\x00" + strconv.Itoa(int(version)), nil
}

func normalizedDomainName(value string) (string, error) {
	value = strings.TrimSuffix(strings.TrimSpace(value), ".")
	if value == "" {
		return "", errors.New("域名不能为空")
	}
	asciiName, err := idna.Lookup.ToASCII(value)
	if err != nil {
		return "", fmt.Errorf("域名格式无效")
	}
	asciiName = strings.ToLower(asciiName)
	if len(asciiName) > MaxDomainBytes {
		return "", fmt.Errorf("域名长度不能超过 %d 字节", MaxDomainBytes)
	}
	for _, label := range strings.Split(asciiName, ".") {
		if len(label) == 0 || len(label) > MaxDomainLabelBytes {
			return "", fmt.Errorf("域名标签长度不能超过 %d 字节", MaxDomainLabelBytes)
		}
	}
	return asciiName, nil
}
