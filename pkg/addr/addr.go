package addr

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
)

// Addr 获取IP地址，通过DUID、系统网卡、URL等方式获取IP地址
// DUID支持OpenWrt软路由系统
// 系统网卡支持获取本地网卡的IP地址
// URL支持通过访问URL获取IP地址
// 返回netip.Addr切片或者error

var (
	// ipv4Reg IPV4地址初筛正则表达式
	ipv4Reg = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)
	// ipv6Reg IPV6地址初筛正则表达式
	ipv6Reg = regexp.MustCompile(`(?:[0-9a-fA-F]{1,4}:){1,7}[0-9a-fA-F]{1,4}|::(?:[0-9a-fA-F]{1,4}:){0,6}[0-9a-fA-F]{1,4}|(?:[0-9a-fA-F]{1,4}:){1,7}:`)
)

// Fetcher 获取IP地址的接口
type Fetcher interface {
	// Fetch 获取IP地址
	// 返回netip.Addr切片或者error
	Fetch(context.Context) ([]netip.Addr, error)
}

func NewFetcher(getType string, getValue string, fetchStrategy ...string) (Fetcher, error) {
	if len(fetchStrategy) > 1 {
		return nil, fmt.Errorf("addr NewFetcher: 获取策略参数过多")
	}
	strategy := ""
	if len(fetchStrategy) == 1 {
		strategy = fetchStrategy[0]
	}
	if getType != "url" && strategy != "" {
		return nil, fmt.Errorf("addr NewFetcher: %s 获取方式不支持获取策略", getType)
	}
	switch getType {
	case "duid":
		return NewDuid(getValue), nil
	case "nic":
		return NewNic(getValue), nil
	case "url":
		return NewUrlWithStrategy(getValue, strategy)
	default:
		return nil, fmt.Errorf("addr NewFetcher: 不支持的获取方式: %s", getType)
	}
}

// extractFromString 从字符串中提取IP地址
func extractFromString(s string) ([]netip.Addr, error) {
	var ips []netip.Addr
	// 提取IPv4地址
	ipv4s := ipv4Reg.FindAllString(s, -1)
	for _, ip := range ipv4s {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		ips = append(ips, addr)
	}

	// 提取IPv6地址
	ipv6s := ipv6Reg.FindAllString(s, -1)
	for _, ip := range ipv6s {
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		ips = append(ips, addr)
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("addr extractFromString:未能从字符串中提取到有效的IP地址")
	}

	return ips, nil
}
