package tencent

import (
	"bytes"
	"context"
	"ddns/pkg/provider"
	"ddns/pkg/utils"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const (
	// dns服务器地址
	host = "dnspod.tencentcloudapi.com"
	// API接口版本
	version = "2021-03-23"
	// API接口路径
	canonicalUri = "/"
	// 签名算法
	algorithm = "TC3-HMAC-SHA256"
	//访问的服务名称
	service     = "dnspod"
	contentType = "application/json; charset=utf-8"
)

type Tencent struct {
	secretId  string
	secretKey string
	client    *http.Client
	limiter   *rate.Limiter
}

func NewTencent(accessKeyId, accessKeySecret string) *Tencent {
	return &Tencent{
		secretId:  accessKeyId,
		secretKey: accessKeySecret,
		client:    &http.Client{Timeout: 15 * time.Second},
		limiter:   rate.NewLimiter(5, 10), // 每秒限制5次请求
	}
}

func (t *Tencent) GetAll(ctx context.Context, domain string, v provider.Version) ([]provider.Record, error) {
	if t.secretId == "" || t.secretKey == "" {
		return nil, fmt.Errorf("Tencent GetAll: secretId 或 secretKey 为空值")
	}
	if domain == "" {
		return nil, fmt.Errorf("Tencent GetAll:domain 为空值")
	}
	payload := struct {
		Domain     string `json:"Domain"`
		RecordType string `json:"RecordType"`
	}{
		Domain:     domain,
		RecordType: v.RecordType(),
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("json序列化请求体失败，err：%v", err)
	}

	res, err := t.do(ctx, "DescribeRecordList", string(jsonPayload))
	if err != nil {
		return nil, err
	}

	return parseResponse(res, domain)
}

func (t *Tencent) GetSub(ctx context.Context, subdomain string, v provider.Version) ([]provider.Record, error) {
	if t.secretId == "" || t.secretKey == "" {
		return nil, fmt.Errorf("Tencent GetAll: secretId 或 secretKey 为空值")
	}
	if subdomain == "" {
		return nil, fmt.Errorf("Tencent GetAll:subdomain 为空值")
	}
	rr, domain, err := utils.ParseDomain(subdomain)
	if err != nil {
		return nil, err
	}
	payload := struct {
		Domain     string `json:"Domain"`
		RecordType string `json:"RecordType"`
		SubDomain  string `json:"SubDomain"`
	}{
		Domain:     domain,
		RecordType: v.RecordType(),
		SubDomain:  rr,
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("json序列化请求体失败，err：%v", err)
	}
	resp, err := t.do(ctx, "DescribeRecordList", string(jsonPayload))
	if err != nil {
		return nil, err
	}
	return parseResponse(resp, domain)
}

// 实现 Creator 接口
func (t *Tencent) Create(ctx context.Context, r *provider.Record) (*provider.Record, error) {
	// 此时传入的 r.RecordId 应该是 ""
	if err := t.addAndUpdate(ctx, r); err != nil {
		return nil, err
	}
	// 执行成功后，addAndUpdate 已经在内部把腾讯云返回的数字 ID 转成字符串填进 r.RecordId 了
	return r, nil
}

// 实现 Updater 接口
func (t *Tencent) Update(ctx context.Context, r *provider.Record) error {
	// 此时传入的 r.RecordId 应该是有具体值的
	return t.addAndUpdate(ctx, r)
}

func (t *Tencent) Delete(ctx context.Context, recordId, domain string) error {
	if t.secretId == "" || t.secretKey == "" {
		return fmt.Errorf("Tencent GetAll: secretId 或 secretKey 为空值")
	}
	if recordId == "" || domain == "" {
		return fmt.Errorf("Tencent Delete:RecordId或Domain 为空值")
	}

	id, err := strconv.ParseInt(recordId, 10, 64)
	if err != nil {
		return fmt.Errorf("addAndUpdate: 转换 RecordId [%s] 为数字失败: %w", recordId, err)
	}

	payload := struct {
		Domain   string `json:"Domain"`
		RecordId int64  `json:"RecordId"`
	}{
		Domain:   domain,
		RecordId: id,
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("json序列化请求体失败，err：%v", err)
	}
	resp, err := t.do(ctx, "DeleteRecord", string(jsonPayload))
	if err != nil {
		return err
	}

	var tempResp struct {
		Response struct {
			Error struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(resp, &tempResp); err != nil {
		return err
	}

	if tempResp.Response.Error.Code != "" {
		return fmt.Errorf("删除记录失败！err:%v", tempResp.Response.Error.Message)
	}

	return nil
}

func (t *Tencent) do(ctx context.Context, action, payload string) ([]byte, error) {
	if err := t.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("do: 请求被取消或超时: %v", err)
	}
	var timestamp = time.Now().Unix()

	authorization := t.sign(action, payload, timestamp)

	url := "https://" + host

	httpRequest, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	httpRequest.Header.Set("Host", host)
	httpRequest.Header.Set("X-TC-Action", action)
	httpRequest.Header.Set("X-TC-Version", version)
	httpRequest.Header.Set("X-TC-Timestamp", strconv.FormatInt(timestamp, 10))
	httpRequest.Header.Set("Content-Type", contentType)
	httpRequest.Header.Set("Authorization", authorization)

	resp, err := t.client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body := &bytes.Buffer{}
	_, err = body.ReadFrom(resp.Body)
	if err != nil {
		return nil, err
	}

	return body.Bytes(), nil
}

func (t *Tencent) addAndUpdate(ctx context.Context, r *provider.Record) error {
	if t.secretId == "" || t.secretKey == "" {
		return fmt.Errorf("Tencent addAndUpdate: secretId 或 secretKey 为空值")
	}

	// 参数校验与兜底
	if r.TTL > 86400 || r.TTL < 1 {
		r.TTL = 600
	}
	if r.RR == "" || r.Type == "" || r.Value == "" {
		return fmt.Errorf("addAndUpdate: 参数不完整！%+v", r)
	}

	//  使用 map 动态构建通用 Payload，完美解决结构体类型固定的问题
	payload := map[string]any{
		"Domain":     r.DomainName,
		"RecordType": r.Type,
		"RecordLine": "默认",
		"Value":      r.Value,
		"SubDomain":  r.RR,
		"TTL":        r.TTL,
	}

	var action string
	if r.RecordId == "" {
		// 创建操作
		action = "CreateRecord"
	} else {
		// 更新操作
		action = "ModifyRecord"
		// 腾讯云修改记录时，RecordId 必须是数字类型(int64)
		id, err := strconv.ParseInt(r.RecordId, 10, 64)
		if err != nil {
			return fmt.Errorf("addAndUpdate: 转换 RecordId [%s] 为数字失败: %w", r.RecordId, err)
		}
		payload["RecordId"] = id
	}

	//  序列化 Payload
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("json序列化请求体失败，err：%v", err)
	}

	// 发起请求
	resp, err := t.do(ctx, action, string(jsonPayload))
	if err != nil {
		return err
	}

	//  统一解析业务错误与成功数据
	var tempResp struct {
		Response struct {
			RecordId  int64  `json:"RecordId"`
			RequestId string `json:"RequestId"`
			Error     struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(resp, &tempResp); err != nil {
		return fmt.Errorf("addAndUpdate: json反序列化错误: %v, API返回: %s", err, string(resp))
	}

	// 拦截腾讯云业务错误
	if tempResp.Response.Error.Code != "" {
		return fmt.Errorf("addAndUpdate: 操作记录失败！: Code=%s, Message=%s (RequestId: %s)",
			tempResp.Response.Error.Code,
			tempResp.Response.Error.Message,
			tempResp.Response.RequestId,
		)
	}

	// 如果是创建操作，把腾讯云生成的数字 ID 转成 string 填回结构体
	if r.RecordId == "" && tempResp.Response.RecordId != 0 {
		r.RecordId = strconv.FormatInt(tempResp.Response.RecordId, 10)
	}

	return nil
}

func parseResponse(res []byte, domain string) ([]provider.Record, error) {
	//  根据腾讯云实际返回的 JSON 结构定义匿名结构体
	var tempResp struct {
		Response struct {
			RecordList []struct {
				RecordId int64  `json:"RecordId"` // 腾讯云返回的是数字类型
				Name     string `json:"Name"`     // 对应主机记录，如 @, www
				Type     string `json:"Type"`     // 记录类型，如 A, CNAME, NS
				Value    string `json:"Value"`    // 记录值
				TTL      int64  `json:"TTL"`      // 生存时间
			} `json:"RecordList"`
			Error struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}

	if err := json.Unmarshal(res, &tempResp); err != nil {
		return nil, fmt.Errorf("parseResponse: json反序列化错误: %v, API返回: %s", err, string(res))
	}

	// 拦截特定错误码，适配通用的 "ErrRecordNotFound" 行为
	if tempResp.Response.Error.Code != "" {
		errCode := tempResp.Response.Error.Code
		// 腾讯云无解析记录时的常见错误码
		if errCode == "ResourceNotFound.NoDataOfRecord" || strings.Contains(errCode, "NotFound") {
			return nil, provider.ErrRecordNotFound
		}
		return nil, fmt.Errorf("parseResponse: API返回错误 [%s]: %s",
			tempResp.Response.Error.Code, tempResp.Response.Error.Message)
	}

	// 检查是否有记录
	if len(tempResp.Response.RecordList) == 0 {
		return nil, provider.ErrRecordNotFound
	}

	// 转换为通用的 provider.Record
	//使用make预分配内存，减少append内存扩容
	records := make([]provider.Record, 0, len(tempResp.Response.RecordList))
	for _, r := range tempResp.Response.RecordList {
		records = append(records, provider.Record{

			RecordId:   strconv.FormatInt(r.RecordId, 10),
			RR:         r.Name,
			Type:       r.Type,
			Value:      r.Value,
			TTL:        r.TTL,
			DomainName: domain,
		})
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("parseResponse: 没有解析到域名记录 ， API返回: %s", string(res))
	}

	return records, nil
}
