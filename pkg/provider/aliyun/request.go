package aliyun

import (
	"time"

	"github.com/google/uuid"
)

// Request 阿里云DNS http请求体
type request struct {
	method       string
	canonicalUri string
	host         string
	xAcsAction   string
	xAcsVersion  string
	headers      map[string]string
	body         []byte
	queryParam   map[string]interface{}
}

func newRequest(method, xAcsAction string) *request {
	req := &request{
		method:       method,
		canonicalUri: "/",
		host:         host,
		xAcsAction:   xAcsAction,
		xAcsVersion:  version,
		headers:      make(map[string]string),
		queryParam:   make(map[string]interface{}),
	}
	req.headers["host"] = host
	req.headers["x-acs-action"] = xAcsAction
	req.headers["x-acs-version"] = version
	req.headers["x-acs-date"] = time.Now().UTC().Format(time.RFC3339)
	req.headers["x-acs-signature-nonce"] = uuid.New().String()
	return req
}
