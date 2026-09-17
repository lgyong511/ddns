package addr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// 通过URL获取IP地址

// Url
const MaxURLSources = 16

type URLStrategy string

const (
	URLStrategyOrdered      URLStrategy = "ordered"
	URLStrategyFirstSuccess URLStrategy = "first-success"
	URLStrategyMajority     URLStrategy = "majority"
)

type Url struct {
	Urls     string
	Strategy URLStrategy
	client   http.Client
}

type urlResult struct {
	index int
	ips   []netip.Addr
	err   error
}

func ParseURLStrategy(value string) (URLStrategy, error) {
	switch URLStrategy(strings.TrimSpace(value)) {
	case "", URLStrategyOrdered:
		return URLStrategyOrdered, nil
	case URLStrategyFirstSuccess:
		return URLStrategyFirstSuccess, nil
	case URLStrategyMajority:
		return URLStrategyMajority, nil
	default:
		return "", fmt.Errorf("URL Fetcher: 不支持的获取策略: %s", value)
	}
}

func NewUrl(urls string) *Url {
	urlFetcher, _ := NewUrlWithStrategy(urls, "")
	return urlFetcher
}

func NewUrlWithStrategy(urls, strategyValue string) (*Url, error) {
	strategy, err := ParseURLStrategy(strategyValue)
	if err != nil {
		return nil, err
	}
	return &Url{
		Urls:     urls,
		Strategy: strategy,
		client: http.Client{
			Timeout: 10 * time.Second,
		},
	}, nil
}

func (u *Url) Fetch(ctx context.Context) ([]netip.Addr, error) {
	urls, err := parseURLs(u.Urls)
	if err != nil {
		return nil, err
	}

	resultCh := make(chan urlResult, len(urls))
	fetchContext, cancel := context.WithCancel(ctx)
	defer cancel()
	for index, targetURL := range urls {
		go func() {
			ips, fetchErr := u.fetchURL(fetchContext, targetURL)
			resultCh <- urlResult{index: index, ips: ips, err: fetchErr}
		}()
	}

	results := make([]urlResult, len(urls))
	var firstSuccess []netip.Addr
	for range urls {
		current := <-resultCh
		results[current.index] = current
		if u.Strategy == URLStrategyFirstSuccess && len(firstSuccess) == 0 && current.err == nil {
			firstSuccess = current.ips
			cancel()
		}
	}
	if len(firstSuccess) > 0 {
		return firstSuccess, nil
	}
	if u.Strategy == URLStrategyMajority {
		return majorityAddresses(results)
	}
	return orderedAddresses(results)
}

func parseURLs(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	urls := make([]string, 0, len(parts))
	for _, part := range parts {
		if targetURL := strings.TrimSpace(part); targetURL != "" {
			urls = append(urls, targetURL)
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("URL Fetcher: 请提供URL地址")
	}
	if len(urls) > MaxURLSources {
		return nil, fmt.Errorf("URL Fetcher: URL 数量不能超过 %d 个", MaxURLSources)
	}
	return urls, nil
}

func ValidateURLs(value string) error {
	_, err := parseURLs(value)
	return err
}

func (u *Url) fetchURL(ctx context.Context, targetURL string) ([]netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("URL Fetcher: HTTP请求失败，状态码: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 1<<20 {
		return nil, fmt.Errorf("URL Fetcher: 响应内容超过 1 MiB 限制")
	}
	return extractFromString(string(body))
}

func orderedAddresses(results []urlResult) ([]netip.Addr, error) {
	var ips []netip.Addr
	var errs []error
	for _, result := range results {
		if result.err != nil {
			errs = append(errs, result.err)
			continue
		}
		ips = append(ips, result.ips...)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("URL Fetcher: 没有解析到IP地址: %w", errors.Join(errs...))
	}
	return ips, nil
}

func majorityAddresses(results []urlResult) ([]netip.Addr, error) {
	votes := make(map[netip.Addr]int)
	var order []netip.Addr
	successes := 0
	for _, result := range results {
		if result.err != nil {
			continue
		}
		successes++
		seen := make(map[netip.Addr]struct{}, len(result.ips))
		for _, ip := range result.ips {
			ip = ip.Unmap()
			if _, exists := seen[ip]; exists {
				continue
			}
			seen[ip] = struct{}{}
			if votes[ip] == 0 {
				order = append(order, ip)
			}
			votes[ip]++
		}
	}
	if successes == 0 {
		return nil, fmt.Errorf("URL Fetcher: 没有成功的 URL 响应")
	}
	majority := successes/2 + 1
	result := make([]netip.Addr, 0, len(order))
	for _, ip := range order {
		if votes[ip] >= majority {
			result = append(result, ip)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("URL Fetcher: %d 个成功端点未形成严格多数", successes)
	}
	return result, nil
}
