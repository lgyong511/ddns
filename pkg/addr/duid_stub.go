//go:build !linux

package addr

import (
	"context"
	"fmt"
	"net/netip"
)

// GetAllDuid 在非 Linux 系统下直接返回错误
func GetAllDuid(ctx context.Context) (map[string][]netip.Addr, error) {
	return nil, fmt.Errorf("DUID 获取方式仅支持 Linux/OpenWrt 系统")
}

// Duid
type Duid struct {
	Duid string
}

// NewDuid 创建一个新的Duid实例
func NewDuid(duid string) *Duid {
	return &Duid{duid}
}

// Fetch 在非 Linux 系统下直接返回错误
func (d *Duid) Fetch(ctx context.Context) ([]netip.Addr, error) {
	return nil, fmt.Errorf("DUID 获取方式仅支持 Linux/OpenWrt 系统")
}
