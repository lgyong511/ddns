package aliyun

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"golang.org/x/exp/maps"

	"net/url"
	"sort"
	"strings"
)

//签名

// sign 签名
func (a *Aliyun) sign(req *request) error {
	// 处理queryParam中参数值为List、Map类型的参数，将参数平铺
	newQueryParams := make(map[string]interface{})
	processObject(newQueryParams, "", req.queryParam)
	req.queryParam = newQueryParams
	// 步骤 1：拼接规范请求串
	canonicalQueryString := ""
	keys := maps.Keys(req.queryParam)
	sort.Strings(keys)
	for _, k := range keys {
		v := req.queryParam[k]
		canonicalQueryString += percentCode(url.QueryEscape(k)) + "=" + percentCode(url.QueryEscape(fmt.Sprintf("%v", v))) + "&"
	}
	canonicalQueryString = strings.TrimSuffix(canonicalQueryString, "&")

	var bodyContent []byte
	if req.body == nil {
		bodyContent = []byte("")
	} else {
		bodyContent = req.body
	}
	hashedRequestPayload := sha256Hex(bodyContent)
	req.headers["x-acs-content-sha256"] = hashedRequestPayload

	canonicalHeaders := ""
	signedHeaders := ""
	HeadersKeys := maps.Keys(req.headers)
	sort.Strings(HeadersKeys)
	for _, k := range HeadersKeys {
		lowerKey := strings.ToLower(k)
		if lowerKey == "host" || strings.HasPrefix(lowerKey, "x-acs-") || lowerKey == "content-type" {
			canonicalHeaders += lowerKey + ":" + req.headers[k] + "\n"
			signedHeaders += lowerKey + ";"
		}
	}
	signedHeaders = strings.TrimSuffix(signedHeaders, ";")

	canonicalRequest := req.method + "\n" + req.canonicalUri + "\n" + canonicalQueryString + "\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + hashedRequestPayload

	// 步骤 2：拼接待签名字符串
	hashedCanonicalRequest := sha256Hex([]byte(canonicalRequest))
	stringToSign := algorithm + "\n" + hashedCanonicalRequest

	// 步骤 3：计算签名
	byteData, err := hmac256([]byte(a.AccessKeySecret), stringToSign)
	if err != nil {
		return err
	}
	signature := strings.ToLower(hex.EncodeToString(byteData))

	// 步骤 4：拼接Authorization
	authorization := algorithm + " Credential=" + a.AccessKeyId + ",SignedHeaders=" + signedHeaders + ",Signature=" + signature
	req.headers["Authorization"] = authorization
	return nil
}

func hmac256(key []byte, toSignString string) ([]byte, error) {
	// 实例化HMAC-SHA256哈希
	h := hmac.New(sha256.New, key)
	// 写入待签名的字符串
	_, err := h.Write([]byte(toSignString))
	if err != nil {
		return nil, err
	}
	// 计算签名并返回
	return h.Sum(nil), nil
}

func sha256Hex(byteArray []byte) string {
	// 实例化SHA-256哈希函数
	hash := sha256.New()
	// 将字符串写入哈希函数
	_, _ = hash.Write(byteArray)
	// 计算SHA-256哈希值并转换为小写的十六进制字符串
	hexString := hex.EncodeToString(hash.Sum(nil))

	return hexString
}

func percentCode(str string) string {
	// 替换特定的编码字符
	str = strings.ReplaceAll(str, "+", "%20")
	str = strings.ReplaceAll(str, "*", "%2A")
	str = strings.ReplaceAll(str, "%7E", "~")
	return str
}

func formDataToString(formData map[string]interface{}) *string {
	tmp := make(map[string]interface{})
	processObject(tmp, "", formData)
	res := ""
	urlEncoder := url.Values{}
	for key, value := range tmp {
		v := fmt.Sprintf("%v", value)
		urlEncoder.Add(key, v)
	}
	res = urlEncoder.Encode()
	return &res
}

// processObject 递归处理对象，将复杂对象（如Map和List）展开为平面的键值对
func processObject(mapResult map[string]interface{}, key string, value interface{}) {
	if value == nil {
		return
	}

	switch v := value.(type) {
	case []interface{}:
		for i, item := range v {
			processObject(mapResult, fmt.Sprintf("%s.%d", key, i+1), item)
		}
	case map[string]interface{}:
		for subKey, subValue := range v {
			processObject(mapResult, fmt.Sprintf("%s.%s", key, subKey), subValue)
		}
	default:
		// if strings.HasPrefix(key, ".") {
		// 	key = key[1:]
		// }
		key = strings.TrimPrefix(key, ".")
		if b, ok := v.([]byte); ok {
			mapResult[key] = string(b)
		} else {
			mapResult[key] = fmt.Sprintf("%v", v)
		}
	}
}
