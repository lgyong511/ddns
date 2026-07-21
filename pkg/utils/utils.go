package utils

import (
	"fmt"
	"strings"

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
