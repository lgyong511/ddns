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

// SubDomainInfo 子域名同步缓存，以子域名为最新缓存对象。
type SubDomainInfo struct {
	//IP地址缓存，即上传同步的
	Addr netip.Addr
	//上次同步的时间
	LastSyncAt time.Time
}

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

	//子域名缓存，key是子域名
	cacheSubDomain := make(map[string]SubDomainInfo)

	p.syncRecord(ctx, record, cacheSubDomain)

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
			p.syncRecord(ctx, record, cacheSubDomain)
		}
	}
}

// syncRecord 同步单个记录的IP地址变化到DNS服务商
func (p *Provider) syncRecord(ctx context.Context, record *config.Record, cacheSubDomain map[string]SubDomainInfo) {
	// logger := slog.With(slog.String("provider", p.provider.Name), slog.String("record", record.Name))
	logger := p.logger(record.Name)

	// 获取当前IP地址
	currentAddr, err := p.fetchCurrentAddr(ctx, record)
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
		//读取缓存
		cache, exists := cacheSubDomain[subDomain]
		//判断是否需要同步
		needUpdate := !exists || cache.Addr != currentAddr || time.Since(cache.LastSyncAt) >= forceInterval*time.Minute
		// 不需要同步，退出当前循环
		if !needUpdate {
			//计算还有多久强制同步
			timeUntilForceSync := time.Until(cache.LastSyncAt.Add(forceInterval * time.Minute))

			logger.Info("跳过同步", "reason", "ip unchanged", "timeUntilForceSync", timeUntilForceSync, "subDomain", subDomain, "currentAddr", currentAddr)
			continue
		}

		// 执行DNS服务商操作
		if err := p.syncToProvider(ctx, subDomain, record, currentAddr); err != nil {
			logger.Error("同步失败", "subDomain", subDomain, "err", err)
			continue // 当前子域名失败，不更新缓存，下一轮重试
		}
		//成功，记录子域名缓存
		cacheSubDomain[subDomain] = SubDomainInfo{Addr: currentAddr, LastSyncAt: time.Now()}
	}
}

// fetchCurrentAddr 获取经过版本过滤、规则筛选后的当前IP地址
func (p *Provider) fetchCurrentAddr(ctx context.Context, record *config.Record) (netip.Addr, error) {
	//获取IP地址
	addrs, err := addr.NewFetcher(record.GetType, record.GetValue).Fetch(ctx)
	if err != nil {
		return netip.Addr{}, err
	}
	//构建版本过滤
	versionFilter := addr.NewFilter(record.IPVersion)
	if versionFilter == nil {
		return netip.Addr{}, fmt.Errorf("无效的 IP 版本: %v", record.IPVersion)
	}
	//执行过滤
	addrs = addr.FilterAddrs(addrs, versionFilter, addr.IsPublic)
	// 构建策略并执行过滤
	addr := addr.NewSelector(record.Rule).Select(addrs)
	if !addr.IsValid() {
		return netip.Addr{}, fmt.Errorf("无效的IP地址：%v", addr)
	}
	return addr, nil
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
