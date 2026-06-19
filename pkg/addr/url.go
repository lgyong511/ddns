package addr

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// 通过URL获取IP地址

// Url
type Url struct {
	Urls   string
	client http.Client
}

func NewUrl(urls string) *Url {
	return &Url{
		Urls: urls,
		client: http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (u *Url) Fetch(ctx context.Context) ([]netip.Addr, error) {
	if u.Urls == "" {
		return nil, fmt.Errorf("URL Fetcher: 请提供URL地址")
	}

	urls := strings.Split(u.Urls, ",")
	if len(urls) == 0 {
		return nil, fmt.Errorf("URL Fetcher: 请提供URL地址")
	}

	type result struct {
		ips []netip.Addr
		err error
	}

	// 并发获取所有 URL 的 IP
	resultCh := make(chan result, len(urls))
	var wg sync.WaitGroup

	for _, url := range urls {
		url := strings.TrimSpace(url)
		if url == "" {
			continue
		}

		wg.Add(1)
		go func(targetURL string) {
			defer wg.Done()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
			if err != nil {
				resultCh <- result{nil, err}
				return
			}

			resp, err := u.client.Do(req)
			if err != nil {
				resultCh <- result{nil, err}
				return
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				resultCh <- result{nil, err}
				return
			}
			ips, err := extractFromString(string(body))
			if err != nil {
				resultCh <- result{nil, err}
				return
			}
			resultCh <- result{ips, nil}
		}(url)
	}

	// 等待所有请求完成
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// 收集结果
	var ips []netip.Addr
	for r := range resultCh {
		if r.ips != nil {
			ips = append(ips, r.ips...)
		}
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("URL Fetcher: 没有解析到IP地址")
	}

	return ips, nil
}
