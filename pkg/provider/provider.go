package provider

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrRecordNotFound = errors.New("record not found")
)

// Getter 域名解析记录获取接口
type Getter interface {
	// GetAll 获取指定域名的所有解析记录
	// version: 0=全部, 4=IPv4, 6=IPv6
	GetAll(context.Context, string, Version) ([]Record, error)
	// GetSub 获取指定域名的指定子域名的解析记录
	//string 子域名，www.lgyong.cc
	// version: 0=全部, 4=IPv4, 6=IPv6
	GetSub(context.Context, string, Version) ([]Record, error)
}

// Updater 域名解析记录更新接口
type Updater interface {
	// Update 更新指定域名解析记录
	Update(context.Context, *Record) error
}

// Creator 域名解析记录创建接口
type Creator interface {
	// Create 创建指定域名解析记录
	Create(context.Context, *Record) (*Record, error)
}

// Deleter 域名解析记录删除接口
type Deleter interface {
	// Delete 删除指定域名解析记录
	Delete(context.Context, string, string) error
}

// Version 解析记录类型版本
type Version int

const (
	IPvAll Version = 0 // 全部
	IPv4   Version = 4
	IPv6   Version = 6
)

// RecordType 获取解析记录类型字符串表示
func (v Version) RecordType() string {
	switch v {
	case IPv4:
		return "A"
	case IPv6:
		return "AAAA"
	default:
		return ""
	}
}

// Record 域名解析记录
type Record struct {
	RecordId   string // 记录ID
	DomainName string // 域名
	RR         string // 记录的子域名部分
	Type       string // A / AAAA / CNAME ...
	Value      string // 记录值，IP地址或CNAME等
	TTL        int64  // 生存时间，单位秒
}

// String 记录信息字符串表示
func (r *Record) String() string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("RecordID=%s, DomainName=%s, RR=%s, Type=%s, Value=%s, TTL=%d", r.RecordId, r.DomainName, r.RR, r.Type, r.Value, r.TTL)
}

// ToJSON 转JSON字符串表示
func (r *Record) ToJSON() string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf(`{"RecordId":"%s", "DomainName":"%s", "RR":"%s", "Type":"%s", "Value":"%s", "TTL":%d}`, r.RecordId, r.DomainName, r.RR, r.Type, r.Value, r.TTL)
}
