package engine

import (
	"context"
	"ddns/pkg/addr"
	"ddns/pkg/config"
	"fmt"
	"net/netip"
	"sync"
	"time"
)

// SubDomainInfo 子域名同步缓存，以子域名为最新缓存对象。
type SubDomainInfo struct {
	//IP地址缓存，即上传同步的
	Addr netip.Addr
	//上次同步的时间
	LastSyncAt time.Time
	//失败次数
	FailCount int
}

// RecordState 管理单个 Record 的 IP 解析器与同步缓存状态
type RecordState struct {
	mu             sync.RWMutex
	fetcher        addr.Fetcher
	filter         addr.Filter
	selector       addr.Selector
	cacheSubDomain map[string]SubDomainInfo
}

func NewRecordState(config *config.Record) (*RecordState, error) {
	fetcher, err := addr.NewFetcher(config.GetType, config.GetValue)
	if err != nil {
		return nil, err
	}
	filter, err := addr.NewFilter(config.IPVersion)
	if err != nil {
		return nil, err
	}
	selector := addr.NewSelector(config.Rule)

	return &RecordState{
		fetcher:  fetcher,
		filter:   filter,
		selector: selector,
		//子域名缓存，key是子域名
		cacheSubDomain: make(map[string]SubDomainInfo),
	}, nil

}

// Resolve 执行 IP 获取和过滤
func (r *RecordState) Resolve(ctx context.Context) (netip.Addr, error) {
	addrs, err := r.fetcher.Fetch(ctx)
	if err != nil {
		return netip.Addr{}, err
	}
	addrs = addr.FilterAddrs(addrs, r.filter, addr.IsPublic)

	addr := r.selector.Select(addrs)
	if !addr.IsValid() {
		return netip.Addr{}, fmt.Errorf("未筛选出有效的公网 IP")
	}

	return addr, nil
}

// ShouldSync 判断子域名是否需要同步，并返回距离下次强制同步的剩余时间
func (r *RecordState) ShouldSync(subDomain string, currentAddr netip.Addr, forceIntervalMinutes time.Duration) (bool, time.Duration) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cache, exists := r.cacheSubDomain[subDomain]
	if !exists || cache.Addr != currentAddr {
		return true, 0
	}

	forceInterval := forceIntervalMinutes * time.Minute
	elapsed := time.Since(cache.LastSyncAt)
	if elapsed >= forceInterval {
		return true, 0
	}

	return false, forceInterval - elapsed
}

// GetCache 获取子域名缓存
func (r *RecordState) GetCache(subDomain string) (SubDomainInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cache, exists := r.cacheSubDomain[subDomain]
	return cache, exists
}

// IncFailCount 增加失败计数并返回最新的失败次数
func (r *RecordState) IncFailCount(SubDomain string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	info := r.cacheSubDomain[SubDomain]
	info.FailCount++
	r.cacheSubDomain[SubDomain] = info
	return info.FailCount
}

// UpdateCache 记录同步成功后的更新缓存
func (r *RecordState) UpdateCache(subDomain string, currentAddr netip.Addr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cacheSubDomain[subDomain] = SubDomainInfo{
		Addr:       currentAddr,
		LastSyncAt: time.Now(),
		//成功后重置失败计数
		FailCount: 0,
	}
}
