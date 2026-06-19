package addr

import (
	"context"
	"fmt"
	"net"
	"net/netip"
)

// Nic 获取网卡信息

// GetAllNic 获取所有网卡的IP地址（包含公网和非公网IP）
func GetAllNic() (map[string][]netip.Addr, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	result := make(map[string][]netip.Addr)
	for _, iface := range interfaces {
		// 1. 跳过非活动接口
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		var ips []netip.Addr
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip, err := netip.ParseAddr(ipNet.IP.String())
			if err != nil {
				continue
			}
			ips = append(ips, ip)
		}

		if len(ips) > 0 {
			result[iface.Name] = ips
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("GetAllNic: 没有网卡有IP地址")
	}

	return result, nil
}

// NIC 根据网卡名字获取IP
type Nic struct {
	Name string
}

// NewNIC 创建一个新的 Nic 获取器，默认返回所有IP地址（包括公网和非公网IP）
func NewNic(name string) *Nic {
	return &Nic{Name: name}
}

// Fetch 满足 Fetcher 接口，通过网卡名获取 IP 地址
// 注意：ctx 参数保留以满足接口签名，网卡查询是本地操作，不依赖上下文
func (n *Nic) Fetch(ctx context.Context) ([]netip.Addr, error) {
	if n.Name == "" {
		return nil, fmt.Errorf("NIC Fetcher: 网卡名字不能为空")
	}

	nicMap, err := GetAllNic()
	if err != nil {
		return nil, err
	}

	ips, ok := nicMap[n.Name]
	if !ok {
		return nil, fmt.Errorf("NIC Fetcher: 网卡 %s 没有IP地址", n.Name)
	}

	return ips, nil
}
