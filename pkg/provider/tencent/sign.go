package tencent

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// sign 生成签名
// action 接口名称
// payload 请求体
// 返回值：authorization 请求头
func (t *Tencent) sign(action, payload string, timestamp int64) (authorization string) {
	httpRequestMethod := "POST"
	canonicalQueryString := ""
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-tc-action:%s\n",
		contentType, host, strings.ToLower(action))
	signedHeaders := "content-type;host;x-tc-action"
	hashedRequestPayload := sha256hex(payload)
	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		httpRequestMethod,
		canonicalUri,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		hashedRequestPayload)

	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	credentialScope := fmt.Sprintf("%s/%s/tc3_request", date, service)
	hashedCanonicalRequest := sha256hex(canonicalRequest)
	string2sign := fmt.Sprintf("%s\n%d\n%s\n%s",
		algorithm,
		timestamp,
		credentialScope,
		hashedCanonicalRequest)

	secretDate := hmacsha256(date, "TC3"+t.secretKey)
	secretService := hmacsha256(service, secretDate)
	secretSigning := hmacsha256("tc3_request", secretService)
	signature := hex.EncodeToString([]byte(hmacsha256(string2sign, secretSigning)))

	authorizationStr := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm,
		t.secretId,
		credentialScope,
		signedHeaders,
		signature)
	return authorizationStr
}

func sha256hex(s string) string {
	b := sha256.Sum256([]byte(s))
	return hex.EncodeToString(b[:])
}

func hmacsha256(s, key string) string {
	hashed := hmac.New(sha256.New, []byte(key))
	hashed.Write([]byte(s))
	return string(hashed.Sum(nil))
}
