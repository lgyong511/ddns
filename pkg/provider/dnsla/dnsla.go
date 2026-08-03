package dnsla

import (
	"bytes"
	"context"
	"ddns/pkg/provider"
	"ddns/pkg/utils"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	host        = "api.dns.la"
	basePath    = "/api"
	contentType = "application/json; charset=utf-8"
)

// DNSLA DNSLA 的 DNS 服务商实现。
type DNSLA struct {
	APIID     string
	APISecret string

	domainIDCache map[string]string
}

func NewDNSLA(apiID, apiSecret string) *DNSLA {
	return &DNSLA{APIID: apiID, APISecret: apiSecret, domainIDCache: make(map[string]string)}
}

func (d *DNSLA) GetAll(ctx context.Context, domain string, version provider.Version) ([]provider.Record, error) {
	if err := d.validate(domain); err != nil {
		return nil, fmt.Errorf("DNSLA GetAll: %w", err)
	}
	return d.list(ctx, domain, "", version)
}

func (d *DNSLA) GetSub(ctx context.Context, subdomain string, version provider.Version) ([]provider.Record, error) {
	if err := d.validate(subdomain); err != nil {
		return nil, fmt.Errorf("DNSLA GetSub: %w", err)
	}
	rr, domain, err := utils.ParseDomain(subdomain)
	if err != nil {
		return nil, err
	}
	return d.list(ctx, domain, rr, version)
}

func (d *DNSLA) list(ctx context.Context, domain, rr string, version provider.Version) ([]provider.Record, error) {
	domainID, err := d.resolveDomainID(ctx, domain)
	if err != nil {
		return nil, err
	}

	query := url.Values{}
	query.Set("pageIndex", "1")
	query.Set("pageSize", "100")
	query.Set("domainId", domainID)
	if rr != "" && rr != "@" {
		query.Set("host", rr)
	}
	if recordType := version.RecordType(); recordType != "" {
		query.Set("type", strconv.Itoa(recordTypeCode(recordType)))
	}
	resp, err := d.do(ctx, http.MethodGet, "/recordList", query, nil)
	if err != nil {
		return nil, err
	}
	return parseRecordListResponse(resp, domain)
}

func (d *DNSLA) Create(ctx context.Context, record *provider.Record) (*provider.Record, error) {
	if record == nil {
		return nil, fmt.Errorf("DNSLA Create: record 为空")
	}
	if err := d.validate(record.DomainName); err != nil {
		return nil, fmt.Errorf("DNSLA Create: %w", err)
	}
	if record.DomainName == "" || record.RR == "" || record.Type == "" || record.Value == "" {
		return nil, fmt.Errorf("DNSLA Create: 记录参数不完整")
	}

	domainID, err := d.resolveDomainID(ctx, record.DomainName)
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"domainId": domainID,
		"type":     recordTypeCode(record.Type),
		"host":     record.RR,
		"data":     record.Value,
		"ttl":      record.TTL,
	}
	resp, err := d.do(ctx, http.MethodPost, "/record", nil, payload)
	if err != nil {
		return nil, err
	}
	var result struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("DNSLA Create: 响应解析失败: %w", err)
	}
	if result.Data.ID != "" {
		record.RecordId = result.Data.ID
	}
	return record, nil
}

func (d *DNSLA) Update(ctx context.Context, record *provider.Record) error {
	if record == nil || record.RecordId == "" {
		return fmt.Errorf("DNSLA Update: RecordId 为空")
	}
	if record.DomainName == "" || record.RR == "" || record.Type == "" || record.Value == "" {
		return fmt.Errorf("DNSLA Update: 记录参数不完整")
	}
	payload := map[string]any{
		"id":   record.RecordId,
		"type": recordTypeCode(record.Type),
		"host": record.RR,
		"data": record.Value,
		"ttl":  record.TTL,
	}
	_, err := d.do(ctx, http.MethodPut, "/record", nil, payload)
	return err
}

func (d *DNSLA) Delete(ctx context.Context, recordID, domain string) error {
	if recordID == "" || domain == "" {
		return fmt.Errorf("DNSLA Delete: RecordId 或 domain 为空")
	}
	query := url.Values{}
	query.Set("id", recordID)
	_, err := d.do(ctx, http.MethodDelete, "/record", query, nil)
	return err
}

func (d *DNSLA) do(ctx context.Context, method, path string, query url.Values, payload any) ([]byte, error) {
	if query == nil {
		query = url.Values{}
	}
	requestURL := "https://" + host + basePath + path
	if encodedQuery := query.Encode(); encodedQuery != "" {
		requestURL += "?" + encodedQuery
	}
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("DNSLA 请求体序列化失败: %w", err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", d.basicAuth())
	request.Header.Set("Content-Type", contentType)
	if method == http.MethodGet {
		request.Header.Set("Accept", "application/json")
	}
	resp, err := provider.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("DNSLA API 返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return responseBody, nil
}

func (d *DNSLA) validate(domain string) error {
	if d.APIID == "" || d.APISecret == "" {
		return fmt.Errorf("APIID 或 APISecret 为空")
	}
	if domain == "" {
		return fmt.Errorf("domain 为空")
	}
	return nil
}

func (d *DNSLA) basicAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(d.APIID+":"+d.APISecret))
}

func (d *DNSLA) resolveDomainID(ctx context.Context, domain string) (string, error) {
	if domain == "" {
		return "", fmt.Errorf("DNSLA resolveDomainID: domain 为空")
	}
	if cachedID := d.domainIDCache[domain]; cachedID != "" {
		return cachedID, nil
	}

	query := url.Values{}
	query.Set("pageIndex", "1")
	query.Set("pageSize", "100")
	query.Set("domain", domain)

	resp, err := d.do(ctx, http.MethodGet, "/domain", query, nil)
	if err != nil {
		return "", fmt.Errorf("DNSLA 获取域名 ID 失败: %w", err)
	}

	id, err := parseDomainIDResponse(resp)
	if err != nil {
		return "", fmt.Errorf("DNSLA 获取域名 ID 失败: %w", err)
	}
	if id == "" {
		return "", fmt.Errorf("DNSLA 未找到域名 %q 的域名 ID", domain)
	}
	if d.domainIDCache != nil {
		d.domainIDCache[domain] = id
	}
	return id, nil
}

func parseDomainIDResponse(body []byte) (string, error) {
	var response struct {
		Code int `json:"code"`
		Data struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Domain string `json:"domain"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("DNSLA 域名列表响应解析失败: %w", err)
	}
	if response.Code != 200 {
		return "", fmt.Errorf("DNSLA API 返回业务错误: code=%d", response.Code)
	}
	if response.Data.ID != "" {
		return response.Data.ID, nil
	}
	if response.Data.Domain != "" {
		return response.Data.Domain, nil
	}
	return "", fmt.Errorf("DNSLA 未在响应中找到域名 ID")
}

func recordTypeCode(recordType string) int {
	switch strings.ToUpper(recordType) {
	case "A":
		return 1
	case "NS":
		return 2
	case "CNAME":
		return 5
	case "MX":
		return 15
	case "TXT":
		return 16
	case "AAAA":
		return 28
	case "SRV":
		return 33
	case "CAA":
		return 257
	case "URL":
		return 256
	default:
		return 0
	}
}

func parseRecordListResponse(body []byte, domain string) ([]provider.Record, error) {
	var response struct {
		Code int `json:"code"`
		Data struct {
			Total   int `json:"total"`
			Results []struct {
				ID       string `json:"id"`
				Host     string `json:"host"`
				Type     int    `json:"type"`
				Data     string `json:"data"`
				TTL      int64  `json:"ttl"`
				Disable  bool   `json:"disable"`
				System   bool   `json:"system"`
				DomainID string `json:"domainId"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("DNSLA 响应解析失败: %w", err)
	}
	if response.Code != 200 {
		return nil, fmt.Errorf("DNSLA API 返回业务错误: code=%d", response.Code)
	}
	if response.Data.Total == 0 || len(response.Data.Results) == 0 {
		return nil, provider.ErrRecordNotFound
	}
	result := make([]provider.Record, 0, len(response.Data.Results))
	for _, item := range response.Data.Results {
		result = append(result, provider.Record{
			RecordId:   item.ID,
			DomainName: domain,
			RR:         item.Host,
			Type:       recordTypeName(item.Type),
			Value:      item.Data,
			TTL:        item.TTL,
		})
	}
	return result, nil
}

func recordTypeName(recordType int) string {
	switch recordType {
	case 1:
		return "A"
	case 2:
		return "NS"
	case 5:
		return "CNAME"
	case 15:
		return "MX"
	case 16:
		return "TXT"
	case 28:
		return "AAAA"
	case 33:
		return "SRV"
	case 257:
		return "CAA"
	case 256:
		return "URL"
	default:
		return ""
	}
}
