package addr

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Selector 选择器，用于选择满足条件的IP地址。

// 规则1，空值或 first 选择第一个IP地址
// 规则2，index@n, 选择第n个IP地址，n从1开始计数，超出范围选择第一个IP地址
// 规则3，splice@n@后缀，选择第n个IP地址的前64位拼接IPv6后缀
// 规则4，contain@substr，选择包含substr的第一个IP地址
// 规则5，prefix@CIDR，选择位于指定网段内的第一个IP地址

// Selector 接口定义了一个Select方法，用于从给定的IP地址列表中选择一个满足特定条件的地址。
// 实现这个接口的类型可以根据不同的选择规则来筛选IP地址，例如选择第n个地址、选择包含特定子串的地址，或者根据IPv6地址的前缀和后缀进行组合选择。
type Selector interface {
	Select(addrs []netip.Addr) netip.Addr
}

// Index 选择器
// 如果索引无效（小于等于0或超过地址列表长度），将默认选择第一个地址。
type Index struct {
	Index int // 选择第n个地址，n从1开始计数
}

// NewIndex 创建一个新的Index选择器，指定要选择的地址索引（从1开始）。
func NewIndex(index int) *Index {
	return &Index{Index: index}
}

// Select 从给定的IP地址列表中选择一个满足条件的地址。
func (s *Index) Select(addrs []netip.Addr) netip.Addr {
	if len(addrs) == 0 {
		return netip.Addr{}
	}
	if s.Index <= 0 || s.Index > len(addrs) {
		return addrs[0]
	}
	return addrs[s.Index-1]
}

// Splice 选择器
// 如果索引无效（小于等于0或超过地址列表长度），将默认选择第一个地址。
// suffix无效时将返回一个空地址。
// addrs列表中的地址必须是IPv6地址，否则将返回一个空地址。
type Splice struct {
	Index  int    // 选择第n个地址，n从1开始计数
	Suffix string // 后缀，可以是8字节的数组、切片，或者标准的IPv6后缀字符串（如 "::1"、“::9209:d0ff:fe09:781d“ 或 "0:0:0:1"）
}

// NewSplice 创建一个新的Splice选择器，指定要选择的地址索引（从1开始）和后缀。
func NewSplice(index int, suffix string) *Splice {
	return &Splice{Index: index, Suffix: suffix}
}

// Select 从给定的IP地址列表中选择一个满足条件的地址，并将其前64位与指定后缀拼接。
func (s *Splice) Select(addrs []netip.Addr) netip.Addr {
	if len(addrs) == 0 {
		return netip.Addr{}
	}
	index := s.Index
	if index <= 0 || index > len(addrs) {
		index = 1
	}
	addr := addrs[index-1]
	if !addr.Is6() {
		return netip.Addr{}
	}
	splicedAddr, err := SpliceIPv6(addr, s.Suffix)
	if err != nil {
		return netip.Addr{}
	}
	return splicedAddr
}

// Contain 选择器
// 从给定的IP地址列表中选择第一个包含指定子串的地址。
type Contain struct {
	Substr string // 要包含的子串
}

// PrefixSelector 从给定的地址列表中选择位于指定网段内的第一个地址。
type PrefixSelector struct {
	Prefix netip.Prefix
}

// NewPrefixSelector 创建一个网段选择器。
func NewPrefixSelector(prefix netip.Prefix) *PrefixSelector {
	return &PrefixSelector{Prefix: prefix.Masked()}
}

func (s *PrefixSelector) Select(addrs []netip.Addr) netip.Addr {
	for _, addr := range addrs {
		if s.Prefix.Contains(addr.Unmap()) {
			return addr
		}
	}
	return netip.Addr{}
}

// NewContain 创建一个新的Contain选择器，指定要包含的子串。
func NewContain(substr string) *Contain {
	return &Contain{Substr: substr}
}

// Select 从给定的IP地址列表中选择第一个包含指定子串的地址。
func (s *Contain) Select(addrs []netip.Addr) netip.Addr {
	for _, addr := range addrs {
		if Contains(s.Substr)(addr) {
			return addr
		}
	}
	return netip.Addr{}
}

// 工厂函数，用于根据规则字符串创建相应的Selector实例。
// 规则字符串的格式如下：
// - 空值或 "first"：选择第一个IP地址。
// - "index@n"：选择第n个IP地址，n从1开始计数。
// - "splice@n@后缀"：选择第n个IP地址的前64位拼接IPv6后缀。
// - "contain@substr"：选择包含substr的第一个IP地址。
// - "prefix@CIDR"：选择位于指定网段内的第一个IP地址。
func NewSelector(rule string) (Selector, error) {
	rule = strings.TrimSpace(rule)
	if rule == "" || rule == "first" {
		return NewIndex(1), nil
	}

	name, value, found := strings.Cut(rule, "@")
	if !found {
		return nil, fmt.Errorf("地址筛选规则无效: %q", rule)
	}
	switch name {
	case "index":
		index, err := parsePositiveIndex(value)
		if err != nil {
			return nil, fmt.Errorf("index 规则无效: %w", err)
		}
		return NewIndex(index), nil
	case "contain":
		if value == "" {
			return nil, fmt.Errorf("contain 规则的匹配文本不能为空")
		}
		return NewContain(value), nil
	case "prefix":
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("prefix 规则的 CIDR 无效: %w", err)
		}
		return NewPrefixSelector(prefix), nil
	case "splice":
		indexValue, suffix, found := strings.Cut(value, "@")
		if !found || suffix == "" {
			return nil, fmt.Errorf("splice 规则必须使用 splice@n@后缀 格式")
		}
		index, err := parsePositiveIndex(indexValue)
		if err != nil {
			return nil, fmt.Errorf("splice 规则索引无效: %w", err)
		}
		if _, err := parseIPv6Suffix(suffix); err != nil {
			return nil, fmt.Errorf("splice 规则后缀无效: %w", err)
		}
		return NewSplice(index, suffix), nil
	default:
		return nil, fmt.Errorf("不支持的地址筛选规则: %s", name)
	}
}

func parsePositiveIndex(value string) (int, error) {
	index, err := strconv.Atoi(value)
	if err != nil || index <= 0 {
		return 0, fmt.Errorf("索引必须是大于 0 的整数")
	}
	return index, nil
}
