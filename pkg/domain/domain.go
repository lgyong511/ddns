package domain

import "errors"

var (
	// ErrDomainNotFound 域名未找到
	ErrDomainNotFound = errors.New("domain not found")
)

// Domain 域名信息
type Domain struct {
	DomainID string // 域名ID
	Domain   string // 域名
}

// Domainer 域名接口
// 用于定义动态域名解析的域名获取
type Domainer interface {
	// GetDomain 获取域名，根据ID获取域名
	GetDomain(domainID string) (string, error)
	// GetDomains 获取域名列表
	GetDomains() ([]Domain, error)
}

// Refresher 刷新域名缓存接口
type Refresher interface {
	Domainer
	// RefreshDomains 刷新域名缓存
	RefreshDomains() error
}

// Record 域名解析记录
type Record struct {
	RecordID string // 记录ID
	Domain   string // 域名
	RR       string // 记录的子域名部分
	Type     string // A / AAAA / CNAME ...
	Value    string // 记录值，IP地址或CNAME等
	TTL      int    // 生存时间，单位秒
}

type Recorder interface {
	GetRecords(domain string) ([]Record, error)         // 获取某个域名下所有记录
	GetRecordByID(recordID string) (*Record, error)     // 精确获取
	GetRecordBySub(domain, rr string) ([]Record, error) // 根据域名和子域名获取记录
	AddRecord(record Record) (string, error)            // 添加，返回新记录ID
	DeleteRecord(recordID string) error                 // 删除指定ID
	UpdateRecord(record Record) error                   // 根据ID更新
}

// 组合接口
type DomainResolver interface {
	Domainer
	Recorder
}
