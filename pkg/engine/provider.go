package engine

import (
	"context"
	"ddns/pkg/addr"
	"ddns/pkg/config"
	"ddns/pkg/provider"
	"ddns/pkg/utils"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"
)

// 子域名同步缓存
type SubDomainInfo struct {
	//IP地址
	Addr netip.Addr
	//上次同步的时间
	LastSyncAt time.Time
}

type Provider struct {
	// 服务商配置
	provider *config.Provider
	//服务商CRUD接口
	operator Operator
	// 强制与DNS服务商比对时间
	forceInterval time.Duration
	//子域名缓存，key是子域名
	cacheSubDomain map[string]SubDomainInfo
}

func NewProvider(provider *config.Provider) (*Provider, error) {
	operator, err := NewOperator(provider.Provider, provider.KeyID, provider.KeySecret)
	if err != nil {
		return nil, err
	}
	return &Provider{
		provider:       provider,
		operator:       operator,
		forceInterval:  30 * time.Minute,
		cacheSubDomain: make(map[string]SubDomainInfo),
	}, nil
}

func (p *Provider) Start(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	//启动所有记录的获取IP地址
	for _, record := range p.provider.Records {
		//把所有记录写入缓存map
		wg.Add(1)
		go p.watchRecord(ctx, &record, wg)
		fmt.Printf("[%s] 记录 %s 监听启动成功！\n", p.provider.Name, record.Name)
	}
}

func (p *Provider) watchRecord(ctx context.Context, record *config.Record, wg *sync.WaitGroup) {
	wg.Done()

	//声明同步函数
	runSync := func() {
		// 获取当前IP地址
		currentAddr, err := p.fetchCurrentAddr(ctx, record)
		if err != nil {
			fmt.Printf("[%s] 获取 IP 失败: %v\n", record.Name, err)
			return
		}
		// 遍历所有子域名
		for _, subDomain := range record.SubDomains {
			//读取缓存
			cache, exists := p.cacheSubDomain[subDomain]
			//判断是否需要同步
			needUpdate := !exists || cache.Addr != currentAddr || time.Since(cache.LastSyncAt) >= p.forceInterval
			// 不需要同步，退出当前循环
			if !needUpdate {
				continue
			}

			// 执行同步，有执行函数判是创建还是更新
			if err := p.syncToProvider(ctx, subDomain, record, currentAddr); err != nil {
				fmt.Printf("[%s] 子域名 %s 同步失败: %v\n", record.Name, subDomain, err)
				continue // 当前子域名失败，不更新缓存，下一轮重试
			}
			//成功，记录子域名缓存
			p.cacheSubDomain[subDomain] = SubDomainInfo{Addr: currentAddr, LastSyncAt: time.Now()}
		}
	}

	//启动执行一次
	runSync()

	//设置定时器
	ticker := time.NewTicker(record.Interval * time.Second)
	defer ticker.Stop()

	//死循环监听ctx和定时器
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("[%s] 收到退出信号，安全退出监听。\n", record.Name)
			return
		case <-ticker.C:
			runSync()
		}
	}
}

// 获取经过版本过滤、规则筛选后的当前IP地址
func (p *Provider) fetchCurrentAddr(ctx context.Context, record *config.Record) (netip.Addr, error) {
	//获取IP地址
	addrs, err := addr.NewFetcher(record.GetType, record.GetValue).Fetch(ctx)
	if err != nil {
		return netip.Addr{}, err
	}
	//构建版本过滤
	versionFilter := addr.NewFilter(record.IPVersion)
	if versionFilter == nil {
		return netip.Addr{}, err
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

func (p *Provider) syncToProvider(ctx context.Context, subDomain string, record *config.Record, currentAddr netip.Addr) error {
	//调用DNS运营商
	resRecords, err := p.operator.GetSub(ctx, subDomain, record.IPVersion)

	// 记录不存在，创建
	if errors.Is(err, addr.ErrNoIPFound) {
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
		if err != nil {
			return err
		}
		fmt.Printf("【创建成功】子域名 [%s] -> %s\n", subDomain, currentAddr)
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
		fmt.Printf("【IP变动/例行同步】更新子域名 [%s] 成功: %s -> %s\n", subDomain, resRecord.Value, currentAddr)
	}
	return nil
}
