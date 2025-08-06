package ip

import "net"

// Iper IP接口
// 用于定义动态域名解析的IP地址获取
type Iper interface {
	// Get 获取到的IP地址是公网IP地址，且经过了过滤后的IP地址
	// 1. 只获取公网IP地址，过滤掉本地IP地址、回环地址、私有IP地址
	// 2. 根据输入的IP地址类型（ipv4或者ipv6）进行过滤
	// 3. 返回的IP地址是经过验证的有效IP地址
	// 4. 当有多个地址时，返回第一个地址，支持@n指定返回第几个，n越界时返回第一个
	// 5. ipv6支持拼接，值是9209:d0ff:fe09:781d，ipv6后缀的，取前缀进行拼接。
	Get() (net.IP, error)
	// GetAll 获取到的IP地址是公网IP地址，且经过了过滤后的IP地址列表
	// 只获取公网IP地址，过滤掉本地IP地址、回环地址、私有IP地址
	GetAll() ([]net.IP, error)
}
