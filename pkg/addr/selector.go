package addr

import (
	"net/netip"
	"strconv"
	"strings"
)

// Selector 选择器，用于选择满足条件的IP地址。

//规则1，空值选择第一个IP地址
//规则2，index@n, 选择第n个IP地址，n从1开始计数，超出范围选择第一个IP地址
//规则3，splice@n@后缀，选择第n个IP地址的前64位拼接后缀，后缀可以是8字节的数组、切片，或者标准的IPv6后缀字符串（如 "::1"、“::9209:d0ff:fe09:781d“ 或 "0:0:0:1"）
//规则4，contain@substr，选择包含substr的第一个IP地址

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
	if s.Index <= 0 || s.Index > len(addrs) {
		s.Index = 1
	}
	addr := addrs[s.Index-1]
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
// - 空值：选择第一个IP地址。
// - "index@n"：选择第n个IP地址，n从1开始计数。
// - "splice@n@后缀"：选择第n个IP地址的前64位拼接后缀，后缀可以是8字节的数组、切片，或者标准的IPv6后缀字符串（如 "::1"、“::9209:d0ff:fe09:781d“ 或 "0:0:0:1"）。
// - "contain@substr"：选择包含substr的第一个IP地址。
func NewSelector(rule string) Selector {
	if rule == "" {
		return &Index{Index: 1}
	}
	if strings.HasPrefix(rule, "index@") {
		indexStr := strings.TrimPrefix(rule, "index@")
		index, err := strconv.Atoi(indexStr)
		if err != nil || index <= 0 {
			return &Index{Index: 1}
		}
		return &Index{Index: index}
	}
	if strings.HasPrefix(rule, "splice@") {
		parts := strings.SplitN(strings.TrimPrefix(rule, "splice@"), "@", 2)
		if len(parts) != 2 {
			return &Splice{Index: 1, Suffix: ""}
		}
		index, err := strconv.Atoi(parts[0])
		if err != nil || index <= 0 {
			index = 1
		}
		return &Splice{Index: index, Suffix: parts[1]}
	}
	if strings.HasPrefix(rule, "contain@") {
		substr := strings.TrimPrefix(rule, "contain@")
		return &Contain{Substr: substr}
	}
	// 都不匹配返回Index选择器，选择第一个IP地址
	return &Index{Index: 1}
}
