package huawei

import (
	"context"
	"ddns/pkg/provider"
	"ddns/pkg/utils"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	DateFormat           = "20060102T150405Z"
	SignAlgorithm        = "SDK-HMAC-SHA256"
	HeaderXDateTime      = "X-Sdk-Date"
	HeaderXHost          = "host"
	HeaderXAuthorization = "Authorization"
	HeaderXContentSha256 = "X-Sdk-Content-Sha256"

	host = "dns.myhuaweicloud.com"
)

type Huawei struct {
	Key     string
	Secret  string
	client  *http.Client
	limiter *rate.Limiter

	// 缓存 ZoneId 并加锁防并发崩溃
	zoneId map[string]string
	mu     sync.RWMutex
}

func NewHuawei(key, secret string) *Huawei {
	return &Huawei{
		Key:     key,
		Secret:  secret,
		client:  &http.Client{Timeout: 15 * time.Second},
		limiter: rate.NewLimiter(5, 10), // 每秒限制5次请求,允许突发10次
		zoneId:  make(map[string]string),
	}
}

func (h *Huawei) GetAll(ctx context.Context, domain string, v provider.Version) ([]provider.Record, error) {
	baseUrl := fmt.Sprintf("https://%s/v2.1/recordsets", host)

	params := url.Values{}
	params.Set("name", domain)
	params.Set("search_mode", "equal")
	if v != provider.IPvAll {
		params.Set("type", v.RecordType())
	}

	fullUrl := fmt.Sprintf("%s?%s", baseUrl, params.Encode())

	resp, err := h.do(ctx, "GET", fullUrl, "")
	if err != nil {
		return nil, err
	}

	return h.parseResponse(resp)
}

func (h *Huawei) GetSub(ctx context.Context, subdomain string, v provider.Version) ([]provider.Record, error) {
	return h.GetAll(ctx, subdomain, v)
}

func (h *Huawei) Update(ctx context.Context, r *provider.Record) error {
	if r.RecordId == "" {
		return fmt.Errorf("Huawei Update: RecordID是空值")
	}

	return h.addAndUpdate(ctx, r)
}

func (h *Huawei) Create(ctx context.Context, r *provider.Record) (*provider.Record, error) {
	if r.DomainName == "" {
		return nil, fmt.Errorf("Huawei Create: DomainName是空值")
	}

	if err := h.addAndUpdate(ctx, r); err != nil {
		return nil, err
	}

	return r, nil
}

func (h *Huawei) Delete(ctx context.Context, recordId, domain string) error {
	// 动态检查 ZoneID，缺失时自动刷一次
	zoneId, err := h.getOrFetchZoneId(ctx, domain)
	if err != nil {
		return fmt.Errorf("Delete 操作失败: %v", err)
	}

	url := fmt.Sprintf("https://%s/v2.1/zones/%s/recordsets/%s", host, zoneId, recordId)
	resp, err := h.do(ctx, "DELETE", url, "")
	if err != nil {
		return err
	}

	var errResp struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(resp, &errResp); err != nil {
		return err
	}
	if errResp.Code != "" {
		return fmt.Errorf("Delete 操作记录失败！: Code=%s, Message=%s", errResp.Code, errResp.Message)
	}

	return nil
}

func (h *Huawei) addAndUpdate(ctx context.Context, r *provider.Record) error {
	if h.Key == "" || h.Secret == "" {
		return fmt.Errorf("addAndUpdate: 凭证不能为空")
	}

	if r.TTL > 86400 || r.TTL < 1 {
		r.TTL = 600
	}
	if r.RR == "" || r.Type == "" || r.Value == "" {
		return fmt.Errorf("addAndUpdate: 参数不完整！%v", r)
	}

	// 动态获取 ZoneID，未找到时自动刷新 API
	zoneId, err := h.getOrFetchZoneId(ctx, r.DomainName)
	if err != nil {
		return fmt.Errorf("addAndUpdate 失败: %v", err)
	}

	var name string
	if r.RR == "@" || r.RR == "" {
		//好像不加.也是可以的
		name = r.DomainName + "."
	} else {
		name = fmt.Sprintf("%s.%s.", r.RR, r.DomainName)
	}

	payload := struct {
		Name    string   `json:"name"`
		Type    string   `json:"type"`
		Records []string `json:"records"`
		Ttl     int64    `json:"ttl"`
	}{
		Name:    name,
		Type:    r.Type,
		Records: []string{r.Value},
		Ttl:     r.TTL,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("JSON 序列化失败: %v", err)
	}

	var resp []byte
	if r.RecordId == "" {
		url := fmt.Sprintf("https://%s/v2.1/zones/%s/recordsets", host, zoneId)
		resp, err = h.do(ctx, "POST", url, string(bodyBytes))
	} else {
		url := fmt.Sprintf("https://%s/v2.1/zones/%s/recordsets/%s", host, zoneId, r.RecordId)
		resp, err = h.do(ctx, "PUT", url, string(bodyBytes))
	}
	if err != nil {
		return err
	}

	var errResp struct {
		ID      string `json:"id"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(resp, &errResp); err != nil {
		return err
	}
	if errResp.Code != "" {
		return fmt.Errorf("addAndUpdate: 操作记录失败！: Code=%s, Message=%s", errResp.Code, errResp.Message)
	}

	if r.RecordId == "" && errResp.ID != "" {
		r.RecordId = errResp.ID
	}

	return nil
}

func (h *Huawei) do(ctx context.Context, action, urlStr string, body string) ([]byte, error) {
	if err := h.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("do: 请求被取消或超时: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, action, urlStr, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Add("content-type", "application/json; charset=utf-8")
	req.Header.Add("x-stage", "RELEASE")

	if err := h.sign(req); err != nil {
		return nil, err
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return respBytes, nil
}

func (h *Huawei) parseResponse(resp []byte) ([]provider.Record, error) {
	var data struct {
		Records []struct {
			RecordId  string   `json:"id"`
			SubDomain string   `json:"name"`
			Type      string   `json:"type"`
			TTL       int64    `json:"ttl"`
			Records   []string `json:"records"`
		} `json:"recordsets"`
		Metadata struct {
			TotalCount int `json:"total_count"`
		} `json:"metadata"`
	}

	if err := json.Unmarshal(resp, &data); err != nil {
		return nil, fmt.Errorf("解析API返回失败，err：%v", err)
	}

	if data.Metadata.TotalCount == 0 {
		return nil, provider.ErrRecordNotFound
	}

	records := make([]provider.Record, 0, len(data.Records))
	for _, Record := range data.Records {
		subDomain := strings.TrimSuffix(Record.SubDomain, ".")
		rr, domainName, err := utils.ParseDomain(subDomain)
		if err != nil {
			continue
		}

		val := ""
		if len(Record.Records) > 0 {
			val = Record.Records[0]
		}

		records = append(records, provider.Record{
			RecordId:   Record.RecordId,
			DomainName: domainName,
			RR:         rr,
			Type:       Record.Type,
			Value:      val,
			TTL:        Record.TTL,
		})
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("parseResponse: 没有解析到域名记录 ， API返回: %s", string(resp))
	}

	return records, nil
}

// 安全地读取/动态刷新 ZoneID 缓存
func (h *Huawei) getOrFetchZoneId(ctx context.Context, domain string) (string, error) {
	h.mu.RLock()
	zoneId, ok := h.zoneId[domain]
	h.mu.RUnlock()

	if ok && zoneId != "" {
		return zoneId, nil
	}

	// 没命中缓存时刷新一次 API
	if err := h.getZoneId(ctx); err != nil {
		return "", err
	}

	h.mu.RLock()
	zoneId, ok = h.zoneId[domain]
	h.mu.RUnlock()

	if !ok || zoneId == "" {
		return "", fmt.Errorf("没有找到域名 %s 对应的 zone_id 缓存", domain)
	}

	return zoneId, nil
}

func (h *Huawei) getZoneId(ctx context.Context) error {
	if h.Key == "" || h.Secret == "" {
		return fmt.Errorf("getZoneId: 凭证不能为空")
	}
	urlStr := fmt.Sprintf("https://%s/v2/zones", host)

	resp, err := h.do(ctx, "GET", urlStr, "")
	if err != nil {
		return err
	}
	var data struct {
		Zones []struct {
			ZoneID   string `json:"id"`
			ZoneName string `json:"name"`
		} `json:"zones"`
	}

	if err := json.Unmarshal(resp, &data); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, zone := range data.Zones {
		name := strings.TrimSuffix(zone.ZoneName, ".")
		h.zoneId[name] = zone.ZoneID
	}
	return nil
}
