package webhook

import (
	"context"
	"ddns/pkg/config"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBodyBytes = 1 << 20

var (
	httpClient = http.Client{Timeout: 5 * time.Second}
)

type WebhookData struct {
	Domain   string
	OldAddr  string
	NewAddr  string
	Provider string
	State    string
	Date     string
}

type Webhook struct {
	*config.Webhook
}

func NewWebhook(webhook *config.Webhook) *Webhook {
	return &Webhook{webhook}
}

func (w *Webhook) Send(ctx context.Context, data *WebhookData) error {
	var req *http.Request
	var err error

	if w.Body == "" {
		// GET 模式：变量值需要进行 URL 转义（URL Encoding）
		replacerGet := strings.NewReplacer(
			"{{Domain}}", url.QueryEscape(data.Domain),
			"{{OldAddr}}", url.QueryEscape(data.OldAddr),
			"{{NewAddr}}", url.QueryEscape(data.NewAddr),
			"{{Provider}}", url.QueryEscape(data.Provider),
			"{{State}}", url.QueryEscape(data.State),
			"{{Date}}", url.QueryEscape(data.Date),
		)
		targetURL := replacerGet.Replace(w.URL)
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
		if err != nil {
			return fmt.Errorf("创建 GET 请求失败: %w", err)
		}
	} else {
		// POST 模式：Body 通常为 JSON 或 普通文本
		escapeJSON := func(s string) string {
			b, err := json.Marshal(s)
			if err != nil {
				return s
			}
			return string(b[1 : len(b)-1])
		}

		replacerPost := strings.NewReplacer(
			"{{Domain}}", escapeJSON(data.Domain),
			"{{OldAddr}}", escapeJSON(data.OldAddr),
			"{{NewAddr}}", escapeJSON(data.NewAddr),
			"{{Provider}}", escapeJSON(data.Provider),
			"{{State}}", escapeJSON(data.State),
			"{{Date}}", escapeJSON(data.Date),
		)
		body := replacerPost.Replace(w.Body)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, w.URL, strings.NewReader(body))
		if err != nil {
			return fmt.Errorf("创建 POST 请求失败: %w", err)
		}
	}

	// 解析并设置 Headers
	if len(w.Headers) != 0 {
		req.Header = w.parseHeaders()
	}

	// 发送 HTTP 请求
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("发送 webhook 请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 修正：读取 resp.Body 而不是 req.Body
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return fmt.Errorf("读取响应内容失败: %w", err)
	}
	if len(respBody) > maxResponseBodyBytes {
		return fmt.Errorf("响应内容超过 %d 字节限制", maxResponseBodyBytes)
	}

	// 校验 HTTP 状态码
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("webhook 返回异常状态码 [%d]: %s", resp.StatusCode, string(respBody))
		slog.Error("webhook 发送失败！", "err", err)
		return err
	}

	// 修正：转为 string 打印日志
	slog.Info("webhook 发送成功！")

	return nil
}

func (w *Webhook) parseHeaders() http.Header {
	headers := make(http.Header)

	for _, item := range w.Headers {
		parts := strings.SplitN(item, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		headers.Set(key, value)
	}

	return headers
}
