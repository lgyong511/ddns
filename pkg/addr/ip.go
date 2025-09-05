package addr

import (
	"fmt"
	"net"
	"strings"
)

type IPType string

const (
	IPv4 IPType = "A"
	IPv6 IPType = "AAAA"
)

// IPGetter IP获取接口
// 用于定义动态域名解析的IP地址获取
type IPGetter interface {
	// Get 获取到的IP地址是公网IP地址，且经过了过滤后的IP地址
	// 1. 只获取公网IP地址，过滤掉本地IP地址、回环地址、私有IP地址
	// 2. 返回的IP地址是经过验证的有效IP地址
	//参数说明：
	// IPType: IP地址类型，支持IPv4和IPv6
	// string: IP地址选取策略，当有多个地址时，返回第一个地址，支持@n指定返回第几个，n越界时返回第一个地址.
	// 如果IPType为ipv6，支持拼接，值是9209:d0ff:fe09:781d，ipv6后缀的，取前缀进行拼接。
	Get(IPType, string) (net.IP, error)
	// GetAll 获取到的IP地址是公网IP地址，且经过了过滤后的IP地址列表
	// 只获取公网IP地址，过滤掉本地IP地址、回环地址、私有IP地址
	GetAll() ([]net.IP, error)
}

// IPVersion 根据IpVersion，返回对应ip版本
func IPVersion(ipType IPType, ip net.IP) (net.IP, error) {
	switch ipType {
	case IPv4:
		if ip.To4() != nil && strings.Contains(ip.String(), ".") {
			return ip, nil
		}
	case IPv6:
		if ip.To16() != nil && strings.Contains(ip.String(), ":") {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("unsupported ip type: %s", ipType)
}

// 处理规则
// 规则1、空值表示默认选择第一个。
// 规则2、值是@开头的后面的数字用于选择第几个IP地址，超出可选范围选择第一个
// 规则3、值是9209:d0ff:fe09:781d，ipv6后缀的，取前缀进行拼接。
// 规则4，正则匹配
func MatchReg(ipType IPType, ips []net.IP, rule string) (net.IP, error) {
	// 遍历IP地址，把符合版本要求的筛选出来
	var newIps []net.IP
	for _, ip := range ips {
		ip, err := IPVersion(ipType, ip)
		if err != nil {
			continue
		}
		newIps = append(newIps, ip)
	}
	// 判断是否有匹配到IP地址
	if len(newIps) == 0 {
		return nil, fmt.Errorf("no matching ip found")
	}
	// 处理规则
	return NewRuler(rule).Match(newIps)
}
