package aliyun

import (
	"ddns/pkg/domain"

	alidns20150109 "github.com/alibabacloud-go/alidns-20150109/v4/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
	"github.com/alibabacloud-go/tea/tea"
	credential "github.com/aliyun/credentials-go/credentials"
)

type Aliyun struct {
	// 阿里云客户端
	client *alidns20150109.Client
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
func (a *Aliyun) GetDomains() ([]domain.Domain, error) {
	request := &alidns20150109.DescribeDomainsRequest{}
	runtime := &util.RuntimeOptions{}
	response, err := a.client.DescribeDomainsWithOptions(request, runtime)
	if err != nil {
		return nil, err
	}
	var domains []domain.Domain
	for _, d := range response.Body.Domains.Domain {
		domains = append(domains, domain.Domain{
			DomainID: *d.DomainId,
			Domain:   *d.DomainName,
		})
	}
	return domains, nil
}

// GetDomainByID 获取域名，根据ID获取域名
func (a *Aliyun) GetDomainByID(domainID string) (string, error) {
	domains, err := a.GetDomains()
	if err != nil {
		return "", err
	}
	for _, d := range domains {
		if d.DomainID == domainID {
			return d.Domain, nil
		}
	}
	return "", domain.ErrDomainNotFound
}

// GetRecords 获取域名解析记录
func (a *Aliyun) GetRecords(domainName string) ([]domain.Record, error) {
	request := &alidns20150109.DescribeDomainRecordsRequest{
		DomainName: &domainName,
	}
	runtime := &util.RuntimeOptions{}
	response, err := a.client.DescribeDomainRecordsWithOptions(request, runtime)
	if err != nil {
		return nil, err
	}
	var records []domain.Record
	for _, r := range response.Body.DomainRecords.Record {
		records = append(records, domain.Record{
			RecordID:   *r.RecordId,
			DomainName: *r.DomainName,
			RR:         *r.RR,
			Type:       *r.Type,
			Value:      *r.Value,
			TTL:        *r.TTL,
		})
	}
	return records, nil
}

// GetRecordByID 获取记录，根据ID获取记录
func (a *Aliyun) GetRecordByID(domainName, recordID string) (*domain.Record, error) {
	records, err := a.GetRecords(domainName)
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		if r.RecordID == recordID {
			return &r, nil
		}
	}
	return nil, domain.ErrRecordNotFound
}

// GetRecordBySub 获取记录，根据子域名获取记录
func (a *Aliyun) GetRecordBySub(subDomainName string) ([]domain.Record, error) {
	describeSubDomainRecordsRequest := &alidns20150109.DescribeSubDomainRecordsRequest{
		SubDomain: tea.String(subDomainName),
	}
	runtime := &util.RuntimeOptions{}
	response, err := a.client.DescribeSubDomainRecordsWithOptions(describeSubDomainRecordsRequest, runtime)
	if err != nil {
		return nil, err
	}
	var records []domain.Record
	for _, r := range response.Body.DomainRecords.Record {
		records = append(records, domain.Record{
			RecordID:   *r.RecordId,
			DomainName: *r.DomainName,
			RR:         *r.RR,
			Type:       *r.Type,
			Value:      *r.Value,
			TTL:        *r.TTL,
		})
	}
	return records, nil
}

// AddRecord 添加记录
// 返回新记录ID
func (a *Aliyun) AddRecord(record domain.Record) (string, error) {
	request := &alidns20150109.AddDomainRecordRequest{
		DomainName: &record.DomainName,
		RR:         &record.RR,
		Type:       &record.Type,
		Value:      &record.Value,
		TTL:        &record.TTL,
	}
	runtime := &util.RuntimeOptions{}
	response, err := a.client.AddDomainRecordWithOptions(request, runtime)
	if err != nil {
		return "", err
	}
	return *response.Body.RecordId, nil
}

// deleteRecord 删除记录
// 根据ID删除记录
func (a *Aliyun) DeleteRecord(recordID string) error {
	request := &alidns20150109.DeleteDomainRecordRequest{
		RecordId: tea.String(recordID),
	}
	runtime := &util.RuntimeOptions{}
	_, err := a.client.DeleteDomainRecordWithOptions(request, runtime)
	if err != nil {
		return err
	}
	return nil
}

func (a *Aliyun) UpdateRecord(record domain.Record) error {
	request := &alidns20150109.UpdateDomainRecordRequest{
		RecordId: tea.String(record.RecordID),
		RR:       &record.RR,
		Type:     &record.Type,
		Value:    &record.Value,
		TTL:      &record.TTL,
	}
	runtime := &util.RuntimeOptions{}
	_, err := a.client.UpdateDomainRecordWithOptions(request, runtime)
	if err != nil {
		return err
	}
	return nil
}
