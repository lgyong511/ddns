package aliyun

import (
	"bytes"
	"context"
	"ddns/pkg/provider"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"golang.org/x/exp/maps"
	"golang.org/x/time/rate"
)

// Create: 新增记录成功返回Record，Update: 无有效数据，Delete: 无有效数据，Get: 返回Record切片

const (
	// dns服务器地址
	host = "alidns.cn-hangzhou.aliyuncs.com"
	// host ="alidns.aliyuncs.com"

	// API接口版本
	version = "2015-01-09"
	// API接口路径
	canonicalUri = "/"
	// 签名算法
	algorithm = "ACS3-HMAC-SHA256"
)

// Aliyun 阿里云DNS
type Aliyun struct {
	AccessKeyId     string
	AccessKeySecret string
	client          *http.Client
	limiter         *rate.Limiter
}

// NewAliyun 新建阿里云DNS
// 参数说明：
// AccessKeyId和AccessKeySecret：阿里云密钥
func NewAliyun(accessKeyId, accessKeySecret string) *Aliyun {
	return &Aliyun{
		AccessKeyId:     accessKeyId,
		AccessKeySecret: accessKeySecret,
		client:          &http.Client{Timeout: 15 * time.Second},
		limiter:         rate.NewLimiter(5, 10), // 每秒5次请求，允许突发10次
	}
}

// GetAll 获取所有域名解析记录
// 参数说明：
// ctx: 上下文，用于控制超时和取消
// string: 域名，例如example.com
// provider.Version: IP地址版本，所有/4/6
// 返回值：provider.[]Record: 记录列表，error: 错误信息，ErrRecordNotFound:没有记录
func (a *Aliyun) GetAll(ctx context.Context, domain string, v provider.Version) ([]provider.Record, error) {
	//验证参数是否合法
	if a.AccessKeyId == "" {
		return nil, fmt.Errorf("Aliyun GetAll: AccessKeyId is empty")
	}
	if a.AccessKeySecret == "" {
		return nil, fmt.Errorf("Aliyun GetAll: AccessKeySecret is empty")
	}
	if domain == "" {
		return nil, fmt.Errorf("Aliyun GetAll: domain is empty")
	}

	//组装请求
	req := newRequest("GET", "DescribeDomainRecords")
	req.queryParam["DomainName"] = domain
	if v != provider.IPvAll {
		req.queryParam["Type"] = v.RecordType()
	}
	if err := a.sign(req); err != nil {
		return nil, fmt.Errorf("Aliyun GetAll: 签名错误: %v", err)
	}

	//发送请求
	res, err := a.do(ctx, req)
	if err != nil {
		return nil, err
	}

	return parseResponse(res)
}

// GetSub 获取域名解析记录
// 参数说明：
// ctx: 上下文，用于控制超时和取消
// string: 子域名，例如www.example.com
// provider.Version: IP地址版本，4/6
// 返回值：provider.[]Record: 记录列表，error: 错误信息，ErrRecordNotFound:没有记录
func (a *Aliyun) GetSub(ctx context.Context, subdomain string, v provider.Version) ([]provider.Record, error) {
	//验证参数是否合法
	if a.AccessKeyId == "" {
		return nil, fmt.Errorf("Aliyun GetSub: AccessKeyId is empty")
	}
	if a.AccessKeySecret == "" {
		return nil, fmt.Errorf("Aliyun GetSub: AccessKeySecret is empty")
	}

	//组装请求
	req := newRequest("GET", "DescribeSubDomainRecords")
	req.queryParam["SubDomain"] = subdomain
	if v != provider.IPvAll {
		req.queryParam["Type"] = v.RecordType()
	}
	if err := a.sign(req); err != nil {
		return nil, fmt.Errorf("Aliyun GetSub: 签名错误: %v", err)
	}

	//发送请求
	res, err := a.do(ctx, req)
	if err != nil {
		return nil, err
	}

	return parseResponse(res)
}

// Update 更新域名解析记录
// 参数说明：
// ctx: 上下文，用于控制超时和取消
// Record: 记录信息，必传RecordID、RR、Type、Value
func (a *Aliyun) Update(ctx context.Context, r *provider.Record) error {
	if r.RecordId == "" {
		return fmt.Errorf("Aliyun Update: RecordID是空值")
	}
	_, err := a.addAndUpdate(ctx, r)
	if err != nil {
		return err
	}

	return nil
}

// Create 创建域名解析记录
// 参数说明：
// ctx: 上下文，用于控制超时和取消
// Record: 记录信息，必传DomainName、RR、Type、Value、TTL
func (a *Aliyun) Create(ctx context.Context, r *provider.Record) (*provider.Record, error) {
	if r.DomainName == "" {
		return nil, fmt.Errorf("Aliyun Create: DomainName是空值")
	}
	res, err := a.addAndUpdate(ctx, r)
	if err != nil {
		return nil, err
	}

	//解析返回，获取RecordID
	var tempResp struct {
		RequestId string `json:"RequestId"`
		RecordId  string `json:"RecordId"`
	}
	if err := json.Unmarshal(res, &tempResp); err != nil {
		return nil, fmt.Errorf("Aliyun Create: 解析 JSON 失败: %v", err)
	}
	if tempResp.RecordId == "" {
		return nil, fmt.Errorf("Aliyun Create: RecordID is empty")
	}
	r.RecordId = tempResp.RecordId
	return r, nil
}

// Delete 删除域名解析记录
// 参数说明：
// ctx: 上下文，用于控制超时和取消
// RecordId: 记录ID
func (a *Aliyun) Delete(ctx context.Context, recordId string) error {
	//验证参数是否合法
	if a.AccessKeyId == "" {
		return fmt.Errorf("Aliyun Delete: AccessKeyId is empty")
	}
	if a.AccessKeySecret == "" {
		return fmt.Errorf("Aliyun Delete: AccessKeySecret is empty")
	}

	req := newRequest("POST", "DeleteDomainRecord")
	req.headers["content-type"] = "application/x-www-form-urlencoded"
	body := map[string]interface{}{
		"RecordId": recordId,
	}
	str := formDataToString(body)
	req.body = []byte(*str)
	// 签名
	if err := a.sign(req); err != nil {
		return fmt.Errorf("Aliyun Delete: 签名失败！: %v", err)
	}
	// 发送请求
	_, err := a.do(ctx, req)
	if err != nil {
		return fmt.Errorf("Aliyun Delete: 请求API失败！: %v", err)
	}

	return nil
}

// addAndUpdate 添加或更新域名解析记录，处理API返回的错误
// TTL 最大值86400,最小值1,建议值600
func (a *Aliyun) addAndUpdate(ctx context.Context, r *provider.Record) ([]byte, error) {
	//验证参数是否合法
	if a.AccessKeyId == "" {
		return nil, fmt.Errorf("addAndUpdate: AccessKeyId is empty")
	}
	if a.AccessKeySecret == "" {
		return nil, fmt.Errorf("addAndUpdate: AccessKeySecret is empty")
	}
	if r.TTL > 86400 || r.TTL < 1 {
		r.TTL = 600
	}
	if r.RR == "" || r.Type == "" || r.Value == "" {
		return nil, fmt.Errorf("addAndUpdate: 参数不完整！%v", r)
	}

	var req *request
	body := make(map[string]interface{})

	//判断是新增记录还是更新记录，更新记录需要传入RecordID
	if r.RecordId == "" { //新增记录
		req = newRequest("POST", "AddDomainRecord")
		body["DomainName"] = r.DomainName
		body["RR"] = r.RR
		body["Type"] = r.Type
		body["Value"] = r.Value
		body["TTL"] = r.TTL
	} else { //更新记录
		req = newRequest("POST", "UpdateDomainRecord")
		body["Type"] = r.Type
		body["RR"] = r.RR
		body["RecordId"] = r.RecordId
		body["Value"] = r.Value
		body["TTL"] = r.TTL
	}

	req.headers["content-type"] = "application/x-www-form-urlencoded"

	str := formDataToString(body)
	req.body = []byte(*str)
	// 签名
	if err := a.sign(req); err != nil {
		return nil, fmt.Errorf("addAndUpdate: 签名失败！: %v", err)
	}
	// 发送请求
	res, err := a.do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("addAndUpdate: 请求API失败！: %v", err)
	}

	// 解析是否返回错误
	var tempResp struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(res, &tempResp); err != nil {
		return nil, fmt.Errorf("addAndUpdate: json反序列化错误: %v, API返回: %s", err, string(res))
	}
	//请求成功Code是没有的，如果Code不为空说明请求失败，返回错误信息
	if tempResp.Code != "" {
		return nil, fmt.Errorf("addAndUpdate: 操作记录失败！: Code=%s, Message=%s", tempResp.Code, tempResp.Message)
	}

	return res, nil
}

// do 发送请求
func (a *Aliyun) do(ctx context.Context, req *request) ([]byte, error) {
	if err := a.limiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("do: 请求被取消或超时: %v", err)
	}
	urlStr := "https://" + req.host + req.canonicalUri
	q := url.Values{}
	keys := maps.Keys(req.queryParam)
	sort.Strings(keys)
	for _, k := range keys {
		v := req.queryParam[k]
		q.Set(k, fmt.Sprintf("%v", v))
	}

	// 组装完整的 URL
	encodedQuery := q.Encode()
	if encodedQuery != "" {
		urlStr += "?" + encodedQuery
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.method, urlStr, bytes.NewReader(req.body))
	if err != nil {
		return nil, err
	}

	for key, value := range req.headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := a.client.Do(httpReq)
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

// 解析返回值，把记录列表转换成domain.Record
// 返回值：[]Record: 记录列表，error: 错误信息，没有记录返回ErrRecordNotFound
func parseResponse(res []byte) ([]provider.Record, error) {
	// --- 使用匿名结构体解析 ---
	var tempResp struct {
		TotalCount    int `json:"TotalCount"`
		DomainRecords struct {
			Record []struct {
				RecordId   string `json:"RecordId"`
				DomainName string `json:"DomainName"`
				RR         string `json:"RR"`
				Type       string `json:"Type"`
				Value      string `json:"Value"`
				TTL        int64  `json:"TTL"`
			} `json:"Record"`
		} `json:"DomainRecords"`
	}

	if err := json.Unmarshal(res, &tempResp); err != nil {
		return nil, fmt.Errorf("parseResponse: json反序列化错误: %v, API返回: %s", err, string(res))
	}

	if tempResp.TotalCount == 0 {
		return nil, provider.ErrRecordNotFound
	}

	// --- 转换为通用 domain.Record ---
	var records []provider.Record
	for _, r := range tempResp.DomainRecords.Record {
		records = append(records, provider.Record{
			RecordId:   r.RecordId,
			DomainName: r.DomainName,
			RR:         r.RR,
			Type:       r.Type,
			Value:      r.Value,
			TTL:        r.TTL,
		})
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("parseResponse: 没有解析到域名记录 ， API返回: %s", string(res))
	}

	return records, nil
}
