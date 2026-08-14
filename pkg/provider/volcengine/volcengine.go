package volcengine

import (
	"bytes"
	"context"
	"ddns/pkg/provider"
	"ddns/pkg/utils"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	host    = "dns.volcengineapi.com"
	version = "2018-08-01"
	service = "dns"
	region  = "cn-beijing"
)

// Volcengine 火山引擎 DNS。
type Volcengine struct {
	AccessKeyID     string
	SecretAccessKey string
}

func NewVolcengine(accessKeyID, secretAccessKey string) *Volcengine {
	return &Volcengine{AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey}
}

func (v *Volcengine) GetAll(ctx context.Context, domain string, recordVersion provider.Version) ([]provider.Record, error) {
	return v.get(ctx, domain, "", recordVersion)
}

func (v *Volcengine) GetSub(ctx context.Context, subdomain string, recordVersion provider.Version) ([]provider.Record, error) {
	rr, domain, err := utils.ParseDomain(subdomain)
	if err != nil {
		return nil, err
	}
	return v.get(ctx, domain, rr, recordVersion)
}

func (v *Volcengine) get(ctx context.Context, domain, rr string, recordVersion provider.Version) ([]provider.Record, error) {
	zoneID, err := v.zoneID(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("Volcengine Get: %w", err)
	}
	body, err := v.do(ctx, http.MethodGet, "ListRecords", url.Values{"ZID": []string{zoneID}}, nil)
	if err != nil {
		return nil, err
	}
	return parseRecords(body, domain, rr, recordVersion.RecordType())
}

func (v *Volcengine) Create(ctx context.Context, record *provider.Record) (*provider.Record, error) {
	if err := validateRecord(record); err != nil {
		return nil, fmt.Errorf("Volcengine Create: %w", err)
	}
	zoneID, err := v.zoneID(ctx, record.DomainName)
	if err != nil {
		return nil, err
	}
	payload := createPayload(record)
	payload["ZID"] = zoneIDValue(zoneID)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	body, err = v.do(ctx, http.MethodPost, "CreateRecord", nil, body)
	if err != nil {
		return nil, err
	}
	var result struct {
		RecordID json.RawMessage `json:"RecordID"`
		Result   struct {
			RecordID json.RawMessage `json:"RecordID"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(body, &result); err == nil {
		record.RecordId = scalarString(result.RecordID)
		if record.RecordId == "" {
			record.RecordId = scalarString(result.Result.RecordID)
		}
	}
	return record, nil
}

func (v *Volcengine) Update(ctx context.Context, record *provider.Record) error {
	if err := validateRecord(record); err != nil {
		return fmt.Errorf("Volcengine Update: %w", err)
	}
	if record.RecordId == "" {
		return fmt.Errorf("Volcengine Update: RecordId 为空")
	}
	zoneID, err := v.zoneID(ctx, record.DomainName)
	if err != nil {
		return err
	}
	payload := updatePayload(record)
	payload["ZID"] = zoneIDValue(zoneID)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = v.do(ctx, http.MethodPost, "UpdateRecord", nil, body)
	return err
}

func (v *Volcengine) Delete(ctx context.Context, recordID, domain string) error {
	if recordID == "" || domain == "" {
		return fmt.Errorf("Volcengine Delete: RecordId 或 domain 为空")
	}
	body, err := json.Marshal(map[string]any{"RecordID": recordID})
	if err != nil {
		return err
	}
	_, err = v.do(ctx, http.MethodPost, "DeleteRecord", nil, body)
	return err
}

func (v *Volcengine) zoneID(ctx context.Context, domain string) (string, error) {
	if v.AccessKeyID == "" || v.SecretAccessKey == "" {
		return "", fmt.Errorf("AccessKeyID 或 SecretAccessKey 为空")
	}
	if domain == "" {
		return "", fmt.Errorf("domain 为空")
	}
	body, err := v.do(ctx, http.MethodGet, "ListZones", nil, nil)
	if err != nil {
		return "", err
	}
	var response struct {
		Zones []struct {
			ZID      json.RawMessage `json:"ZID"`
			ZoneID   json.RawMessage `json:"ZoneID"`
			ZoneName string          `json:"ZoneName"`
			Name     string          `json:"Name"`
		} `json:"Zones"`
		Result struct {
			Zones []struct {
				ZID      json.RawMessage `json:"ZID"`
				ZoneID   json.RawMessage `json:"ZoneID"`
				ZoneName string          `json:"ZoneName"`
				Name     string          `json:"Name"`
			} `json:"Zones"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("火山引擎区域响应解析失败: %w", err)
	}
	zones := response.Zones
	if len(zones) == 0 {
		zones = response.Result.Zones
	}
	for _, zone := range zones {
		zoneID := scalarString(zone.ZID)
		if zoneID == "" {
			zoneID = scalarString(zone.ZoneID)
		}
		zoneName := zone.ZoneName
		if zoneName == "" {
			zoneName = zone.Name
		}
		if normalizeName(zoneName) == normalizeName(domain) && zoneID != "" {
			return zoneID, nil
		}
	}
	return "", fmt.Errorf("火山引擎区域不存在 %q，API返回: %s: %w", domain, string(body), provider.ErrRecordNotFound)
}

func (v *Volcengine) doJSON(ctx context.Context, action string, payload map[string]any) ([]byte, error) {
	return v.doJSONAction(ctx, action, payload)
}

func (v *Volcengine) doJSONAction(ctx context.Context, action string, payload map[string]any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return v.do(ctx, http.MethodPost, action, nil, body)
}

func (v *Volcengine) do(ctx context.Context, method, action string, query url.Values, body []byte) ([]byte, error) {
	if query == nil {
		query = url.Values{}
	}
	query.Set("Action", action)
	query.Set("Version", version)
	requestURL := "https://" + host + "/?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Host = host
	v.sign(request)
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
		return nil, fmt.Errorf("火山引擎 API 返回 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var response struct {
		Error struct {
			Code    string `json:"Code"`
			Message string `json:"Message"`
		} `json:"Error"`
		ResponseMetadata struct {
			Error struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
	}
	if json.Unmarshal(responseBody, &response) == nil {
		apiError := response.Error
		if apiError.Code == "" {
			apiError = response.ResponseMetadata.Error
		}
		if apiError.Code != "" {
			return nil, fmt.Errorf("火山引擎 API 错误: %s: %s", apiError.Code, apiError.Message)
		}
	}
	return responseBody, nil
}

func parseRecords(body []byte, domain, wantedRR, wantedType string) ([]provider.Record, error) {
	var response struct {
		Records []struct {
			RecordID json.RawMessage `json:"RecordID"`
			Host     string          `json:"Host"`
			Type     string          `json:"Type"`
			Value    string          `json:"Value"`
			TTL      int64           `json:"TTL"`
		} `json:"Records"`
		Result struct {
			Records []struct {
				RecordID json.RawMessage `json:"RecordID"`
				Host     string          `json:"Host"`
				Type     string          `json:"Type"`
				Value    string          `json:"Value"`
				TTL      int64           `json:"TTL"`
			} `json:"Records"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("火山引擎记录响应解析失败: %w", err)
	}
	records := response.Records
	if len(records) == 0 {
		records = response.Result.Records
	}
	result := make([]provider.Record, 0, len(records))
	for _, item := range records {
		if wantedRR != "" && !recordHostMatches(item.Host, wantedRR, domain) {
			continue
		}
		if wantedType != "" && !strings.EqualFold(item.Type, wantedType) {
			continue
		}
		rr := item.Host
		if parsedRR, parsedDomain, err := utils.ParseDomain(strings.TrimSuffix(item.Host, ".")); err == nil && normalizeName(parsedDomain) == normalizeName(domain) {
			rr = parsedRR
		}
		result = append(result, provider.Record{RecordId: scalarString(item.RecordID), DomainName: domain, RR: rr, Type: item.Type, Value: item.Value, TTL: item.TTL})
	}
	if len(result) == 0 {
		return nil, provider.ErrRecordNotFound
	}
	return result, nil
}

func validateRecord(record *provider.Record) error {
	if record == nil || record.DomainName == "" || record.RR == "" || record.Type == "" || record.Value == "" {
		return fmt.Errorf("记录参数不完整")
	}
	return nil
}

func createPayload(record *provider.Record) map[string]any {
	return map[string]any{"Host": record.RR, "Type": record.Type, "Value": record.Value, "Line": "default", "TTL": record.TTL}
}

func updatePayload(record *provider.Record) map[string]any {
	return map[string]any{
		"RecordID": record.RecordId,
		"Host":     record.RR,
		"Type":     record.Type,
		"Value":    record.Value,
		"Line":     "default",
		"TTL":      record.TTL,
	}
}

func zoneIDValue(zoneID string) int64 {
	value, _ := strconv.ParseInt(zoneID, 10, 64)
	return value
}

func scalarString(value json.RawMessage) string {
	return strings.Trim(string(value), `"`)
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

func recordHostMatches(host, wantedRR, domain string) bool {
	host = normalizeName(host)
	wantedRR = normalizeName(wantedRR)
	if host == wantedRR {
		return true
	}
	return host == normalizeName(wantedRR+"."+domain)
}

var _ provider.Getter = (*Volcengine)(nil)
var _ provider.Creator = (*Volcengine)(nil)
var _ provider.Updater = (*Volcengine)(nil)
var _ provider.Deleter = (*Volcengine)(nil)
