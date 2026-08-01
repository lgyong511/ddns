package baidu

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type signRequest struct {
	method     string
	path       string
	query      url.Values
	headers    map[string]string
	timestamp  time.Time
	expireTime int
}

func hmacSHA256Hex(key []byte, value string) string {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

func uriEncode(value string, encodeSlash bool) string {
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			builder.WriteByte(c)
		} else if c == '/' && !encodeSlash {
			builder.WriteByte(c)
		} else {
			fmt.Fprintf(&builder, "%%%02X", c)
		}
	}
	return builder.String()
}

func (b *Baidu) sign(req *signRequest) string {
	if req.timestamp.IsZero() {
		req.timestamp = time.Now().UTC()
	}
	if req.expireTime <= 0 {
		req.expireTime = 1800
	}

	timestamp := req.timestamp.UTC().Format("2006-01-02T15:04:05Z")
	prefix := fmt.Sprintf("bce-auth-v1/%s/%s/%d", b.AccessKeyId, timestamp, req.expireTime)

	queryKeys := make([]string, 0, len(req.query))
	for key := range req.query {
		if !strings.EqualFold(key, "authorization") {
			queryKeys = append(queryKeys, key)
		}
	}
	sort.Strings(queryKeys)
	var queryParts []string
	for _, key := range queryKeys {
		values := append([]string(nil), req.query[key]...)
		sort.Strings(values)
		if len(values) == 0 {
			queryParts = append(queryParts, uriEncode(key, true)+"=")
			continue
		}
		for _, value := range values {
			queryParts = append(queryParts, uriEncode(key, true)+"="+uriEncode(value, true))
		}
	}

	var headerKeys []string
	canonicalHeaders := make(map[string]string)
	for key, value := range req.headers {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if lowerKey != "host" && lowerKey != "content-type" && lowerKey != "content-md5" && !strings.HasPrefix(lowerKey, "x-bce-") {
			continue
		}
		headerKeys = append(headerKeys, lowerKey)
		canonicalHeaders[lowerKey] = uriEncode(lowerKey, true) + ":" + uriEncode(strings.TrimSpace(value), true)
	}
	sort.Strings(headerKeys)
	var headerParts []string
	for _, key := range headerKeys {
		headerParts = append(headerParts, canonicalHeaders[key])
	}

	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s", strings.ToUpper(req.method), uriEncode(req.path, false), strings.Join(queryParts, "&"), strings.Join(headerParts, "\n"))
	signingKey := hmacSHA256Hex([]byte(b.SecretAccessKey), prefix)
	signature := hmacSHA256Hex([]byte(signingKey), canonicalRequest)
	return fmt.Sprintf("%s/%s/%s", prefix, strings.Join(headerKeys, ";"), signature)
}
