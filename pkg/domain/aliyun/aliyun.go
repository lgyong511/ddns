package aliyun

import (
	"ddns/pkg/domain"
	"time"

	alidns20150109 "github.com/alibabacloud-go/alidns-20150109/v4/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	credential "github.com/aliyun/credentials-go/credentials"
)

type Aliyun struct {
	// 阿里云客户端
	client *alidns20150109.Client
	// 域名缓存
	domains []domain.Domain
	//缓存时间
	cacheTime time.Time
}

// NewAliyun 创建新的阿里云客户端
func NewAliyun(id, secret string) (*Aliyun, error) {
	// 工程代码建议使用更安全的无AK方式，凭据配置方式请参见：https://help.aliyun.com/document_detail/378661.html。
	conf := new(credential.Config).
		SetType("access_key").
		SetAccessKeyId(id).
		SetAccessKeySecret(secret)
	credential, err := credential.NewCredential(conf)
	if err != nil {
		return nil, err
	}
	config := &openapi.Config{
		Credential: credential,
	}
	// Endpoint 请参考 https://api.aliyun.com/product/Alidns
	// client = &alidns20150109.Client{}
	client, err := alidns20150109.NewClient(config)
	if err != nil {
		return nil, err
	}
	return &Aliyun{
		client: client,
	}, nil
}

// GetDomains 获取域名列表
// 如果缓存过期则重新获取
func (a *Aliyun) GetDomains() ([]domain.Domain, error) {
	if a.domains == nil || time.Since(a.cacheTime) > time.Hour {
		err := a.getDomains()
		if err != nil {
			return nil, err
		}
	}
	return a.domains, nil
}

// GetDomain 获取域名，根据ID获取域名
func (a *Aliyun) GetDomain(domainID string) (string, error) {
	for _, d := range a.domains {
		if d.DomainID == domainID {
			return d.Domain, nil
		}
	}
	return "", domain.ErrDomainNotFound
}

// RefreshDomains 刷新域名缓存
func (a *Aliyun) RefreshDomains() error {
	return a.getDomains()
}

// getDomains 获取域名列表，并记录缓存时间
func (a *Aliyun) getDomains() error {
	request := &alidns20150109.DescribeDomainsRequest{}
	runtime := &util.RuntimeOptions{}
	response, err := a.client.DescribeDomainsWithOptions(request, runtime)
	if err != nil {
		return err
	}
	var domains []domain.Domain
	for _, d := range response.Body.Domains.Domain {
		domains = append(domains, domain.Domain{
			DomainID: *d.DomainId,
			Domain:   *d.DomainName,
		})
	}
	a.domains = domains
	a.cacheTime = time.Now()
	return nil
}
