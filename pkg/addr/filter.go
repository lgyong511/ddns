package addr

import (
	"ddns/pkg/provider"
	"fmt"
	"net/netip"
	"strings"
)

// Filter 过滤IP地址：去重、过滤无效地址、过滤指定版本IP地址

// Filter 函数类型，接受一个 netip.Addr 类型的参数，返回一个 bool 类型的结果，表示该地址是否满足过滤条件。
type Filter func(addr netip.Addr) bool

func NewFilter(v provider.Version) Filter {
	switch v {
	case provider.IPv4:
		return IsIPv4
	case provider.IPv6:
		return IsIPv6
	default:
		return nil
	}
}

// IsIPv4 是否IPv4地址的过滤函数
func IsIPv4(addr netip.Addr) bool {
	return addr.Is4()
}

// IsIPv6 是否IPv6地址的过滤函数
func IsIPv6(addr netip.Addr) bool {
	return addr.Is6()
}

// IsPublic 是否公网IP地址的过滤函数
func IsPublic(addr netip.Addr) bool {
	return addr.IsValid() && !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() && !addr.IsLinkLocalMulticast()
}

// Contains 是否包含substr字符串IP地址的过滤函数
func Contains(substr string) Filter {
	return func(addr netip.Addr) bool {
		return addr.String() != "" && strings.Contains(addr.String(), substr)
	}
}

// FilterAddrs 过滤IP地址列表，返回满足过滤条件的地址列表
// 默认过滤无效地址和重复地址，可以通过传入自定义的 Filter 函数来实现更多的过滤条件，例如过滤指定版本的IP地址。
func FilterAddrs(addrs []netip.Addr, filters ...Filter) []netip.Addr {
	seen := make(map[netip.Addr]struct{})
	result := make([]netip.Addr, 0, len(addrs))

	for _, addr := range addrs {
		addr = addr.Unmap()

		if !addr.IsValid() {
			continue
		}

		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}

		keep := true
		for _, filter := range filters {
			if !filter(addr) {
				keep = false
				break
			}
		}

		if keep {
			result = append(result, addr)
		}
	}

	return result
}

// SpliceIPv6 取 IPv6 地址的前 64 位前缀，拼接指定的后缀。
// suffix 可以是 8 字节的数组、切片，或者标准的 IPv6 后缀字符串（如 "::1"、“::9209:d0ff:fe09:781d“ 或 "0:0:0:1"）
func SpliceIPv6(addr netip.Addr, suffix string) (netip.Addr, error) {
	// 统一解包并确保是 IPv6
	addr = addr.Unmap()
	if !addr.Is6() {
		return netip.Addr{}, fmt.Errorf("SpliceIPv6: IP地址不是IPv6")
	}

	// 解析后缀地址（例如将 "::1"、“::9209:d0ff:fe09:781d“ 解析为标准的 netip.Addr）
	suffixAddr, err := netip.ParseAddr(suffix)
	if err != nil {
		// 移除开头可能存在的任意多个冒号（兼容 ":" 或 "::"）
		cleanSuffix := strings.TrimLeft(suffix, ":")

		// 统一在前面加上标准的双冒号 "::" 重新解析
		var retryErr error
		suffixAddr, retryErr = netip.ParseAddr("::" + cleanSuffix)
		if retryErr != nil {
			return netip.Addr{}, fmt.Errorf("SpliceIPv6: 后缀格式非法: %w", err)
		}
	}

	//  提取两者的字节数组
	ipBytes := addr.As16()           // 原始 IP 的 16 字节
	suffixBytes := suffixAddr.As16() // 后缀 IP 的 16 字节

	// 组合：前 8 字节用原始前缀，后 8 字节用后缀的后 8 字节
	var finalBytes [16]byte
	copy(finalBytes[0:8], ipBytes[0:8])       // 复制前 64 位前缀
	copy(finalBytes[8:16], suffixBytes[8:16]) // 复制后 64 位后缀

	// 重新生成 Addr 对象（带上原始的 Zone，如果有的话）
	return netip.AddrFrom16(finalBytes).WithZone(addr.Zone()), nil
}
