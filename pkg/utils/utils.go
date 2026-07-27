package utils

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// ParseDomain 精准切分复杂子域名
// 返回值：rr (主机记录), domain (主域名), err
func ParseDomain(fullDomain string) (rr string, domain string, err error) {
	// 利用公认的 PSL 列表，直接提取出最底层、可注册的主域名 (e.g., "baidu.com", "google.com.cn")
	domain, err = publicsuffix.EffectiveTLDPlusOne(fullDomain)
	if err != nil {
		return "", "", fmt.Errorf("解析主域名失败: %v", err)
	}

	// 如果全量域名和主域名完全一样，说明它本身就是主域名，没有 RR 部分（即 @）
	if fullDomain == domain {
		return "@", domain, nil
	}

	// 将全量域名去掉主域名部分，剩下的就是 RR
	// 例如：fullDomain = "a.b.c.baidu.com", domain = "baidu.com"
	// 裁剪后得到 "a.b.c."
	suffix := "." + domain
	if strings.HasSuffix(fullDomain, suffix) {
		rr = strings.TrimSuffix(fullDomain, suffix)
	}

	return rr, domain, nil
}

// DoWithRetry 尝试执行 fn，如果失败则重试
// maxRetries 最大重试次数
// retryInterval 重新间隔时间，单位秒
func DoWithRetry(ctx context.Context, maxRetries int, retryInterval time.Duration, fn func() error) error {
	var err error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// 如果上下文已取消/超时，直接退出，不再重试
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err = fn()
		if err == nil {
			return nil // 执行成功，直接返回
		}

		// 如果未达到最大重试次数，进行重试准备
		if attempt < maxRetries {

			if !isTimeoutErr(err) {
				return err
			}
			slog.Warn("API 请求失败，准备重试",
				"attempt", attempt+1,
				"maxRetries", maxRetries,
				"err", err,
			)

			// 等待重试间隔或上下文取消
			select {
			case <-time.After(retryInterval):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return fmt.Errorf("请求重试 %d 次后仍然失败: %w", maxRetries, err)
}

// DoWithDefaultRetry 默认快捷重试（重试 1 次，间隔 3 秒）
func DoWithDefaultRetry(ctx context.Context, fn func() error) error {
	return DoWithRetry(ctx, 1, 3*time.Second, fn)
}

// isTimeoutErr 判断错误是否为超时（Context 超时或网络 Timeout）
func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}

	// 检查是否为 Context 超时
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// 检查是否实现了 net.Error 的 Timeout 方法
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// 兼容部分自定义带 Timeout() bool 方法的错误
	type timeouter interface {
		Timeout() bool
	}
	var te timeouter
	if errors.As(err, &te) && te.Timeout() {
		return true
	}

	// 某些第三方库/HTTP客户端将超时信息拼在错误文本中
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded")
}
