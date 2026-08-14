package baidu

import (
	"bytes"
	"context"
	"ddns/pkg/provider"
	"ddns/pkg/utils"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const host = "dns.baidubce.com"

type Baidu struct {
	AccessKeyId     string
	SecretAccessKey string
}

func NewBaidu(accessKeyId, secretAccessKey string) *Baidu {
	return &Baidu{AccessKeyId: accessKeyId, SecretAccessKey: secretAccessKey}
}

func (b *Baidu) GetAll(ctx context.Context, domain string, v provider.Version) ([]provider.Record, error) {
	return b.get(ctx, domain, "", v)
}

func (b *Baidu) GetSub(ctx context.Context, subdomain string, v provider.Version) ([]provider.Record, error) {
	rr, domain, err := utils.ParseDomain(subdomain)
	if err != nil {
		return nil, err
	}
	return b.get(ctx, domain, rr, v)
}

func (b *Baidu) get(ctx context.Context, domain, rr string, v provider.Version) ([]provider.Record, error) {
	if err := b.validate(domain); err != nil {
		return nil, fmt.Errorf("Baidu Get: %w", err)
	}
	query := url.Values{}
	if rr != "" {
		query.Set("rr", rr)
	}
	if recordType := v.RecordType(); recordType != "" {
		query.Set("type", recordType)
	}
	resp, err := b.do(ctx, http.MethodGet, recordPath(domain), query, nil)
	if err != nil {
		return nil, err
	}
	return parseResponse(resp, domain, v.RecordType())
}

func (b *Baidu) Create(ctx context.Context, record *provider.Record) (*provider.Record, error) {
	if err := b.validate(recordDomain(record)); err != nil {
		return nil, fmt.Errorf("Baidu Create: %w", err)
	}
	if err := validateRecord(record); err != nil {
		return nil, fmt.Errorf("Baidu Create: %w", err)
	}
	body, err := json.Marshal(recordPayload(record))
	if err != nil {
		return nil, err
	}
	resp, err := b.do(ctx, http.MethodPost, recordPath(record.DomainName), nil, body)
	if err != nil {
		return nil, err
	}
	var result struct {
		RecordID string `json:"recordId"`
		ID       string `json:"id"`
		Result   struct {
			RecordID string `json:"recordId"`
			ID       string `json:"id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &result); err == nil {
		if result.RecordID != "" {
			record.RecordId = result.RecordID
		} else if result.ID != "" {
			record.RecordId = result.ID
		} else if result.Result.RecordID != "" {
			record.RecordId = result.Result.RecordID
		} else {
			record.RecordId = result.Result.ID
		}
	}
	return record, nil
}

func (b *Baidu) Update(ctx context.Context, record *provider.Record) error {
	if err := b.validate(recordDomain(record)); err != nil {
		return fmt.Errorf("Baidu Update: %w", err)
	}
	if record.RecordId == "" {
		return fmt.Errorf("Baidu Update: RecordId 为空")
	}
	if err := validateRecord(record); err != nil {
		return fmt.Errorf("Baidu Update: %w", err)
	}
	body, err := json.Marshal(recordPayload(record))
	if err != nil {
		return err
	}
	_, err = b.do(ctx, http.MethodPut, recordPath(record.DomainName)+"/"+url.PathEscape(record.RecordId), nil, body)
	return err
}

func (b *Baidu) Delete(ctx context.Context, recordID, domain string) error {
	if err := b.validate(domain); err != nil {
		return fmt.Errorf("Baidu Delete: %w", err)
	}
	if recordID == "" || domain == "" {
		return fmt.Errorf("Baidu Delete: RecordId 或 domain 为空")
	}
	_, err := b.do(ctx, http.MethodDelete, recordPath(domain)+"/"+url.PathEscape(recordID), nil, nil)
	return err
}

func (b *Baidu) do(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, error) {
	if query == nil {
		query = url.Values{}
	}
	now := time.Now().UTC()
	headers := map[string]string{
		"Host":         host,
		"Content-Type": "application/json;charset=utf-8",
		"x-bce-date":   now.Format("2006-01-02T15:04:05Z"),
	}
	authorization := b.sign(&signRequest{method: method, path: path, query: query, headers: headers, timestamp: now})
	reqURL := "https://" + host + path
	if encodedQuery := query.Encode(); encodedQuery != "" {
		reqURL += "?" + encodedQuery
	}
	request, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("Authorization", authorization)
	resp, err := provider.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := provider.ReadResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("百度云 API 返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return responseBody, nil
}

func (b *Baidu) validate(domain string) error {
	if b.AccessKeyId == "" || b.SecretAccessKey == "" {
		return fmt.Errorf("AccessKeyId 或 SecretAccessKey 为空")
	}
	if domain == "" {
		return fmt.Errorf("domain 为空")
	}
	return nil
}

func recordPath(domain string) string {
	return "/v1/dns/zone/" + url.PathEscape(domain) + "/record"
}

func validateRecord(record *provider.Record) error {
	if record == nil || record.DomainName == "" || record.RR == "" || record.Type == "" || record.Value == "" {
		return fmt.Errorf("记录参数不完整")
	}
	return nil
}

func recordDomain(record *provider.Record) string {
	if record == nil {
		return ""
	}
	return record.DomainName
}

func recordPayload(record *provider.Record) map[string]any {
	return map[string]any{"rr": record.RR, "type": record.Type, "value": record.Value, "ttl": record.TTL, "line": "default"}
}

func parseResponse(body []byte, domain, recordType string) ([]provider.Record, error) {
	var response struct {
		Records []struct {
			ID       string `json:"id"`
			RecordID string `json:"recordId"`
			RR       string `json:"rr"`
			Type     string `json:"type"`
			Value    string `json:"value"`
			TTL      int64  `json:"ttl"`
		} `json:"records"`
		Result struct {
			Records []struct {
				ID       string `json:"id"`
				RecordID string `json:"recordId"`
				RR       string `json:"rr"`
				Type     string `json:"type"`
				Value    string `json:"value"`
				TTL      int64  `json:"ttl"`
			} `json:"records"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("百度云响应解析失败: %w", err)
	}
	records := response.Records
	if len(records) == 0 {
		records = response.Result.Records
	}
	if len(records) == 0 {
		return nil, provider.ErrRecordNotFound
	}
	result := make([]provider.Record, 0, len(records))
	for _, record := range records {
		if recordType != "" && !strings.EqualFold(record.Type, recordType) {
			continue
		}
		recordID := record.RecordID
		if recordID == "" {
			recordID = record.ID
		}
		result = append(result, provider.Record{RecordId: recordID, DomainName: domain, RR: record.RR, Type: record.Type, Value: record.Value, TTL: record.TTL})
	}
	if len(result) == 0 {
		return nil, provider.ErrRecordNotFound
	}
	return result, nil
}
