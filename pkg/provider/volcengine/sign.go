package volcengine

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const contentType = "application/json; charset=UTF-8"

// sign 为火山引擎请求生成 HMAC-SHA256 签名并设置认证请求头。
func (v *Volcengine) sign(request *http.Request) {
	xDate := time.Now().UTC().Format("20060102T150405Z")
	body := requestBody(request)
	bodyHash := sha256Hex(body)
	signedHeaders := "host;x-date"
	canonicalHeaders := "host:" + host + "\nx-date:" + xDate
	if len(body) > 0 {
		signedHeaders = "host;x-content-sha256;x-date"
		canonicalHeaders = "host:" + host + "\nx-content-sha256:" + bodyHash + "\nx-date:" + xDate
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("X-Content-Sha256", bodyHash)
	}
	canonical := strings.Join([]string{
		request.Method,
		"/",
		request.URL.RawQuery,
		canonicalHeaders,
		"",
		signedHeaders,
		bodyHash,
	}, "\n")
	shortDate := xDate[:8]
	scope := strings.Join([]string{shortDate, region, service, "request"}, "/")
	stringToSign := strings.Join([]string{
		"HMAC-SHA256",
		xDate,
		scope,
		sha256Hex([]byte(canonical)),
	}, "\n")
	kDate := hmacSHA256([]byte(v.SecretAccessKey), shortDate)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	request.Header.Set("X-Date", xDate)
	request.Header.Set("Authorization", fmt.Sprintf(
		"HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		v.AccessKeyID, scope, signedHeaders, signature,
	))
}

func requestBody(request *http.Request) []byte {
	if request.Body == nil {
		return nil
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	return body
}

func sha256Hex(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

func hmacSHA256(key []byte, content string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(content))
	return mac.Sum(nil)
}
