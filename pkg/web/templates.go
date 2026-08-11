package web

import (
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed templates/*.html static/style.css static/logo.svg
var content embed.FS

func parseTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"join":          strings.Join,
		"providerLabel": providerLabel,
		"mask":          mask,
		"maskWebhook":   maskWebhook,
		"compactValue":  compactValue,
		"durNumber":     durNumber,
		"inc":           func(i int) int { return i + 1 },
	}
	return template.New("").Funcs(funcs).ParseFS(content, "templates/*.html")
}

func (s *Server) style(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	data, err := content.ReadFile("static/style.css")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

func (s *Server) logo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	data, err := content.ReadFile("static/logo.svg")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

func providerLabel(value string) string {
	labels := map[string]string{
		"aliyun": "阿里云", "baidu": "百度云", "dnsla": "DNSLA",
		"tencent": "腾讯云", "huawei": "华为云", "volcengine": "火山引擎",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return "未选择"
}

func mask(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "未配置"
	}
	if len(value) <= 6 {
		return value[:1] + "****"
	}
	return value[:3] + "****" + value[len(value)-3:]
}

func maskWebhook(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return maskSensitiveParts(value)
	}
	query := parsed.Query()
	for key, values := range query {
		if isSecretQueryKey(key) {
			for i, item := range values {
				values[i] = mask(item)
			}
			query[key] = values
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func isSecretQueryKey(key string) bool {
	key = strings.ToLower(key)
	return key == "key" || key == "token" || key == "access_token" || key == "signature" || key == "sign" || strings.Contains(key, "secret")
}

func maskSensitiveParts(value string) string {
	for _, marker := range []string{"key=", "token=", "access_token=", "signature=", "sign="} {
		idx := strings.Index(strings.ToLower(value), marker)
		if idx < 0 {
			continue
		}
		start := idx + len(marker)
		end := strings.IndexAny(value[start:], "& \t\r\n")
		if end < 0 {
			end = len(value)
		} else {
			end += start
		}
		return value[:start] + mask(value[start:end]) + value[end:]
	}
	return value
}

func compactValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "默认预设"
	}
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= 96 {
		return value
	}
	return value[:93] + "..."
}

func durNumber(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case time.Duration:
		return int64(v)
	default:
		return 0
	}
}
