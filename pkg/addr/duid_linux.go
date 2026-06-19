//go:build linux

package addr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"time"
)

// Duid 获取IP地址，只适用于在软路由OpenWrt上获取IPv6地址

// GetAllDuid 获取所有DUID和对应的IP地址列表，返回一个map，key是DUID，value是IP地址切片
func GetAllDuid(ctx context.Context) (map[string][]net.IP, error) {
	// 设置一个超时时间，防止ctx没有设置超时和命令执行时间过长
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", "ubus call dhcp ipv6leases").Output()
	if err != nil {
		return nil, err
	}

	var data struct {
		Device struct {
			BrLan struct {
				Leases []struct {
					Duid     string `json:"duid"`
					Ipv6Addr []struct {
						Address string `json:"address"`
					} `json:"ipv6-addr"`
				} `json:"leases"`
			} `json:"br-lan"`
		} `json:"device"`
	}
	err = json.Unmarshal(out, &data)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]net.IP)
	for _, lease := range data.Device.BrLan.Leases {
		var ips []net.IP

		for _, addr := range lease.Ipv6Addr {
			if ip := net.ParseIP(addr.Address); ip != nil {
				ips = append(ips, ip)
			}
		}
		if len(ips) > 0 {
			result[lease.Duid] = ips
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("Duid Fetcher: 没有解析到IP地址")
	}

	return result, nil
}

// Duid
type Duid struct {
	Duid string
}

// NewDuid 创建一个新的Duid实例
func NewDuid(duid string) *Duid {
	return &Duid{duid}
}

// Fetch 根据DUID获取对应的IP地址列表，如果没有找到对应的IP地址，则返回错误
func (d *Duid) Fetch(ctx context.Context) ([]net.IP, error) {
	if d.Duid == "" {
		return nil, fmt.Errorf("Duid Fetcher: 请提供duid")
	}

	duidMap, err := GetAllDuid(ctx)
	if err != nil {
		return nil, err
	}

	ips, ok := duidMap[d.Duid]
	if !ok {
		return nil, fmt.Errorf("Duid Fetcher: duid %s 没有IP地址", d.Duid)
	}

	return ips, nil
}
