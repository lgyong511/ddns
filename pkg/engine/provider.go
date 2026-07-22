package engine

import (
	"context"
	"ddns/pkg/addr"
	"ddns/pkg/config"
	"ddns/pkg/provider"
	"ddns/pkg/utils"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"
)

// Provider 代表一个DNS服务商实例，包含服务商配置和操作接口
type Provider struct {
	// 服务商配置
	provider *config.Provider
	//服务商CRUD接口
	operator Operator
}

// NewProvider 创建一个新的 Provider 实例
func NewProvider(provider *config.Provider) (*Provider, error) {
	operator, err := NewOperator(provider.Provider, provider.KeyID, provider.KeySecret)
	if err != nil {
		return nil, err
	}

	return &Provider{
		provider: provider,
		operator: operator,
	}, nil
}

// Start 启动 Provider，监听所有记录的IP地址变化，并同步到DNS服务商
func (p *Provider) Start(ctx context.Context) {
	var wg sync.WaitGroup
	//启动所有记录的获取IP地址
	for _, record := range p.provider.Records {
		wg.Add(1)
		r := record
		go func(record *config.Record) {
			defer wg.Done()
			p.watchRecord(ctx, record)
		}(&r)
		slog.Info("record 监听已启动", "provider", p.provider.Name, "record", record.Name)
	}

	<-ctx.Done()
	slog.Info("Provider 正在退出", "provider", p.provider.Name)

	wg.Wait()
	slog.Info("Provider 已退出", "provider", p.provider.Name)
}

// watchRecord 监听单个记录的IP地址变化，并同步到DNS服务商
func (p *Provider) watchRecord(ctx context.Context, record *config.Record) {
	recordState, err := NewRecordState(record)
	if err != nil {
		slog.Error("初始化 RecordState 失败", "err", err)
		return
	}

	p.syncRecord(ctx, record, recordState)

	//设置定时器
	//允许范围是5-60秒
	interval := record.Interval
	if interval < 5 || interval > 60 {
		interval = 10
	}
	ticker := time.NewTicker(interval * time.Second)
	defer ticker.Stop()

	//死循环监听ctx和定时器
	for {
		select {
		case <-ctx.Done():
			slog.Info("record 监听已停止", "record", record.Name)
			return
		case <-ticker.C:
			p.syncRecord(ctx, record, recordState)
		}
	}
}

// syncRecord 同步单个记录的IP地址变化到DNS服务商
func (p *Provider) syncRecord(ctx context.Context, record *config.Record, recordState *RecordState) {
	logger := p.logger(record.Name)

	// 获取当前IP地址
	currentAddr, err := recordState.Resolve(ctx)
	if err != nil {
		logger.Error("获取 IP 失败", "err", err)
		return
	}

	//强制同步时间，单位分钟
	//允许范围在1-30分钟
	forceInterval := p.provider.ForceInterval
	if forceInterval < 1 || forceInterval > 30 {
		forceInterval = 5
	}

	// 遍历所有子域名
	for _, subDomain := range record.SubDomains {
		//判断是否需要更新和计算剩余强制和DNS服务商对齐时间
		needSync, timeUntilForceSync := recordState.ShouldSync(subDomain, currentAddr, forceInterval)
		if !needSync {
			logger.Info("跳过同步", "subDomain", subDomain, "currentAddr", currentAddr, "reason", "ip unchanged", "timeUntilForceSync", timeUntilForceSync.Truncate(time.Second))
			continue
		}

		// 执行DNS服务商操作
		if err := p.syncToProvider(ctx, subDomain, record, currentAddr); err != nil {
			logger.Error("同步失败", "subDomain", subDomain, "err", err)
			continue // 当前子域名失败，不更新缓存，下一轮重试
		}

		// 同步成功，封装好的缓存更新
		recordState.UpdateCache(subDomain, currentAddr)
	}
}

// syncToProvider 同步子域名记录到DNS服务商
func (p *Provider) syncToProvider(ctx context.Context, subDomain string, record *config.Record, currentAddr netip.Addr) error {
	logger := p.logger(record.Name)
	//调用DNS运营商
	resRecords, err := p.operator.GetSub(ctx, subDomain, record.IPVersion)

	// 记录不存在，创建
	if errors.Is(err, provider.ErrRecordNotFound) {
		// 切割rr domain
		rr, domain, err := utils.ParseDomain(subDomain)
		if err != nil {
			return err
		}
		//创建记录
		_, err = p.operator.Create(ctx, &provider.Record{
			Type:       record.IPVersion.RecordType(),
			RR:         rr,
			DomainName: domain,
			Value:      currentAddr.String(),
			TTL:        record.TTL,
		})
		if err == nil {

			logger.Info("DNS记录已创建", "subDomain", subDomain, "currentAddr", currentAddr)
		}
		return err
	}

	// 其他错误
	if err != nil {
		return err
	}

	//记录存在，更新
	for _, resRecord := range resRecords {
		//DNS服务商返回的和本地当前IP地址相同，跳过更新
		if resRecord.Value == currentAddr.String() {
			logger.Info("云端解析记录未改变，无需更新", "subDomain", subDomain, "currentAddr", currentAddr)
			continue
		}
		reqRecord := resRecord
		reqRecord.Value = currentAddr.String()
		if err := p.operator.Update(ctx, &reqRecord); err != nil {
			return fmt.Errorf("更新记录失败: %w", err)
		}
		logger.Info("DNS记录已更新", "subDomain", subDomain, "oldAddr", resRecord.Value, "newAddr", currentAddr)
	}
	return nil
}

// logger 返回一个带有 provider 和 record 字段的 slog.Logger 实例，用于记录日志
func (p *Provider) logger(record string) *slog.Logger {
	return slog.With(
		"provider", p.provider.Name,
		"record", record,
	)
}

// SubDomainInfo 子域名同步缓存，以子域名为最新缓存对象。
type SubDomainInfo struct {
	//IP地址缓存，即上传同步的
	Addr netip.Addr
	//上次同步的时间
	LastSyncAt time.Time
}

// RecordState 管理单个 Record 的 IP 解析器与同步缓存状态
type RecordState struct {
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

// UpdateCache 更新成功后的同步缓存
func (r *RecordState) UpdateCache(subDomain string, currentAddr netip.Addr) {
	r.cacheSubDomain[subDomain] = SubDomainInfo{
		Addr:       currentAddr,
		LastSyncAt: time.Now(),
	}
}
