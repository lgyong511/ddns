package addr

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// FilterIP 过滤 IP 地址，返回指定类型的 IP 地址列表
func FilterIP(ips []net.IP, ipType Type) ([]net.IP, error) {
	var result []net.IP
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		if ipType == IP4 && ip.To4() != nil {
			result = append(result, ip)
		} else if ipType == IP6 && ip.To4() == nil && ip.To16() != nil {
			result = append(result, ip)
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no %s ip found", ipType)
	}
	return result, nil
}

// 规则1、空值表示默认选择第一个。
// 规则2、值是@开头的后面的数字用于选择第几个IP地址，超出可选范围选择第一个
// 规则3、值是9209:d0ff:fe09:781d/64，ipv6后缀的，取前缀进行拼接，给定了前缀长度按给定长度拼接，默认按64
// 规则4，筛选
// Ruler 规则接口
type Ruler interface {
	Match(ips []net.IP) (net.IP, error)
}

// Default 默认规则，对应规则1
type Default struct {
}

func NewDefault() *Default {
	return &Default{}
}

func (d *Default) Match(ips []net.IP) (net.IP, error) {
	return ips[0], nil
}

// Select 选择规则，对应规则2
type Select struct {
	index int
}

func NewSelect(index int) *Select {
	return &Select{
		index: index,
	}
}

func (s *Select) Match(ips []net.IP) (net.IP, error) {
	if s.index < 0 || s.index >= len(ips) {
		return ips[0], nil
	}
	return ips[s.index], nil
}

// Splice 拼接规则，对应规则3
type Splice struct {
	prefixLen  int
	identifier string
}

func NewSplice(prefixLen int, identifier string) *Splice {
	return &Splice{prefixLen: prefixLen, identifier: identifier}
}

func (s *Splice) Match(ips []net.IP) (net.IP, error) {
	if s.prefixLen < 0 || s.prefixLen > 128 {
		return nil, fmt.Errorf("invalid prefix length: %d", s.prefixLen)
	}
	// 简化实现：要求按字节对齐（/8 的倍数），常见 /48 /56 /64 /80 /96 等都覆盖
	if s.prefixLen%8 != 0 {
		return nil, fmt.Errorf("prefix length must be multiple of 8, got /%d", s.prefixLen)
	}

	suffixBytes, err := parseIPv6SuffixBytes(s.identifier)
	if err != nil {
		return nil, fmt.Errorf("invalid suffix %q: %w", s.identifier, err)
	}
	start := s.prefixLen / 8

	for _, ip := range ips {
		ip16 := ip.To16()
		if ip16 == nil || ip.To4() != nil {
			continue // 只处理 IPv6
		}
		out := make([]byte, 16)
		copy(out, ip16)
		// 用后缀覆盖掉主机位（从 start 起的所有字节）
		copy(out[start:], suffixBytes[start:])
		return net.IP(out), nil
	}
	return nil, fmt.Errorf("no ipv6 ip to splice for suffix: %s", s.identifier)
}

// --- helpers ---

// 把 identifier 解析成 16 字节：
// - 不含 "::" 时，作为“后缀”右对齐到 8 个 hextet（16 字节）。
// - 含 "::" 时，按 IPv6 规则把中间的零段展开，得到完整 8 个 hextet。
func parseIPv6SuffixBytes(id string) ([]byte, error) {
	b := make([]byte, 16)
	if id == "" {
		return b, nil // 空后缀 => 全零
	}

	var hextets [8]uint16
	switch {
	case strings.Contains(id, "::"):
		parts := strings.SplitN(id, "::", 2)
		var left, right []string
		if parts[0] != "" {
			left = strings.Split(parts[0], ":")
		}
		if parts[1] != "" {
			right = strings.Split(parts[1], ":")
		}
		if len(left)+len(right) > 8 {
			return nil, fmt.Errorf("too many hextets in suffix")
		}
		// 填左边
		idx := 0
		for _, seg := range left {
			v, err := parseHextet(seg)
			if err != nil {
				return nil, err
			}
			hextets[idx] = v
			idx++
		}
		// 填 0（由 :: 展开）
		zeros := 8 - (len(left) + len(right))
		idx += zeros
		// 填右边（靠右）
		for i := 0; i < len(right); i++ {
			v, err := parseHextet(right[i])
			if err != nil {
				return nil, err
			}
			hextets[idx+i] = v
		}

	default:
		// 无 :: ，把显式后缀右对齐
		segs := strings.Split(id, ":")
		if len(segs) > 8 {
			return nil, fmt.Errorf("too many hextets in suffix")
		}
		start := 8 - len(segs) // 右对齐位置
		for i, seg := range segs {
			v, err := parseHextet(seg)
			if err != nil {
				return nil, err
			}
			hextets[start+i] = v
		}
	}

	// hextets -> 16 字节
	for i := 0; i < 8; i++ {
		b[2*i] = byte(hextets[i] >> 8)
		b[2*i+1] = byte(hextets[i])
	}
	return b, nil
}

func parseHextet(s string) (uint16, error) {
	if s == "" {
		return 0, fmt.Errorf("empty hextet")
	}
	if len(s) > 4 {
		return 0, fmt.Errorf("hextet too long: %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid hextet %q: %w", s, err)
	}
	return uint16(v), nil
}

// Filter 过滤规则，对应规则4
type Filter struct {
	substr string
}

func NewFilter(substr string) *Filter {
	return &Filter{
		substr: substr,
	}
}

func (f *Filter) Match(ips []net.IP) (net.IP, error) {
	for _, ip := range ips {
		if strings.Contains(ip.String(), f.substr) {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("no ip match filter: %s", f.substr)
}

func NewRuler(rule string) Ruler {
	switch {
	case rule == "":
		return NewDefault()
	case strings.HasPrefix(rule, "@"):
		var n int
		fmt.Sscanf(rule, "@%d", &n)
		return NewSelect(n - 1)
	case strings.Contains(rule, ":"): // 简单判断 IPv6 拼接
		parts := strings.Split(rule, "/")
		prefixLen, err := strconv.Atoi(parts[1])
		if len(parts) != 2 || prefixLen < 0 || prefixLen > 128 || err != nil {
			return NewSplice(64, rule) // 默认 /64
		}
		return NewSplice(prefixLen, parts[0])
	default: // 走过滤规则
		return NewFilter(rule)
	}
}
