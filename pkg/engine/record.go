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
	//下次重试等待间隔
	NextRetryGap time.Duration
	//下一次强制同步时间
	NextForceInterval time.Duration
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

// ShouldSync 子域名是否需要同步处理
// 参数：子域名，当前IP地址，最大与DNS API同步时间
// 返回值：是否同步，剩余同步时间
func (r *RecordState) ShouldSync(subDomain string, currentAddr netip.Addr) (bool, time.Duration) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cache, exists := r.cacheSubDomain[subDomain]

	// 首次触发，刚启动没有缓存
	if !exists {
		return true, 0
	}

	//检查是否已过设定的退避时间
	elapsed := time.Since(cache.LastSyncAt)
	if cache.FailCount > 0 {
		if elapsed >= cache.NextRetryGap {
			return true, 0
		}
		return false, cache.NextRetryGap - elapsed
	}

	// IP地址变了，触发同步
	if cache.Addr != currentAddr {
		return true, 0
	}

	// IP地址没变，时间到了最大同步时间，防止其他方式改变了云端记录
	forceInterval := cache.NextForceInterval
	if forceInterval <= 0 {
		forceInterval = 1 * time.Minute
	}
	if elapsed >= forceInterval {
		return true, 0
	}

	// 剩余时间不足5秒时，触发同步
	// 已经到了触发同步时间了，但是计算有毫秒级误差
	if forceInterval-elapsed <= 5*time.Second {
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

// IncFailCount 同步失败状态处理
// 参数：子域名和最大与DNS API同步时间
// 返回：失败次数和重试时间
func (r *RecordState) IncFailCount(SubDomain string, maxIntervalMinutes time.Duration) (int, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info := r.cacheSubDomain[SubDomain]
	info.FailCount++

	//缓存不存在时，刷新失败发生的时间点
	info.LastSyncAt = time.Now()

	//计算下次重试时间，以30秒为基准。
	nextGap := time.Duration(info.FailCount) * 30 * time.Second
	maxInterval := maxIntervalMinutes * time.Minute
	//最长不能大于最大同步时间
	if nextGap > maxInterval {
		nextGap = maxInterval
	}
	info.NextRetryGap = nextGap

	//写入缓存
	r.cacheSubDomain[SubDomain] = info
	return info.FailCount, info.NextRetryGap
}

// UpdateCache 记录同步成功后的更新缓存
func (r *RecordState) UpdateCache(subDomain string, currentAddr netip.Addr, maxForceMinutes time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	oldCache, exists := r.cacheSubDomain[subDomain]
	baseInterval := 1 * time.Minute
	maxInterval := maxForceMinutes * time.Minute
	var nextInterval time.Duration

	if !exists || oldCache.Addr != currentAddr {
		nextInterval = baseInterval
	} else {
		nextInterval = oldCache.NextForceInterval + 1*time.Minute
		nextInterval = min(nextInterval, maxInterval)
	}

	r.cacheSubDomain[subDomain] = SubDomainInfo{
		Addr:       currentAddr,
		LastSyncAt: time.Now(),
		//成功后重置失败计数
		FailCount:         0,
		NextRetryGap:      0,
		NextForceInterval: nextInterval,
	}
}
