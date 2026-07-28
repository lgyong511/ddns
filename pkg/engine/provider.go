package engine

import (
	"context"
	"ddns/pkg/config"
	"ddns/pkg/provider"
	"ddns/pkg/utils"
	"ddns/pkg/webhook"
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
	// Webhook 通知器
	notifier *webhook.Webhook
}

// NewProvider 创建一个新的 Provider 实例
func NewProvider(provider *config.Provider, notifier *webhook.Webhook) (*Provider, error) {
	operator, err := NewOperator(provider.Provider, provider.KeyID, provider.KeySecret)
	if err != nil {
		return nil, err
	}

	return &Provider{
		provider: provider,
		operator: operator,
		notifier: notifier,
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
	if interval < 10 || interval > 60 {
		interval = 10
	}

	//新建定时器
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
	if forceInterval < 5 || forceInterval > 30 {
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

		//获取缓存的IP地址
		oldAddr := netip.Addr{}
		if cache, ok := recordState.GetCache(subDomain); ok {
			oldAddr = cache.Addr
		}

		// 执行DNS服务商操作
		if err := p.syncToProvider(ctx, subDomain, record, currentAddr, oldAddr); err != nil {
			// 获取失败计数
			failCount, nextRetryGap := recordState.IncFailCount(subDomain, forceInterval)
			msg := fmt.Sprintf("第%d次同步失败!", failCount)
			logger.Error(msg, "subDomain", subDomain, "err", err, "nextRetryGap", nextRetryGap.Truncate(time.Second))

			if failCount == 1 || failCount%3 == 0 {
				//连续同步3次失败发送 webhook 通知
				p.sendNotification(&webhook.WebhookData{
					Domain:   subDomain,
					OldAddr:  oldAddr.String(),
					NewAddr:  currentAddr.String(),
					Provider: p.provider.Provider,
					State:    fmt.Sprintf("第%d次同步失败 err: %v nextRetryGap:%v", failCount, err, nextRetryGap.Truncate(time.Second)),
					Date:     time.Now().Format("2006-01-02 15:04:05"),
				})
			}

			continue // 当前子域名操作失败，不更新缓存，下一轮重试
		}

		// 同步成功，更新缓存和重置失败计数器
		recordState.UpdateCache(subDomain, currentAddr)
	}
}

// syncToProvider 同步子域名记录到DNS服务商
func (p *Provider) syncToProvider(ctx context.Context, subDomain string, record *config.Record, currentAddr netip.Addr, oldAddr netip.Addr) error {
	logger := p.logger(record.Name)

	// 调用dns api 获取记录信息
	var resRecords []provider.Record
	err := utils.DoWithDefaultRetry(ctx, func() error {
		var err error
		//调用DNS运营商
		resRecords, err = p.operator.GetSub(ctx, subDomain, record.IPVersion)
		if errors.Is(err, provider.ErrRecordNotFound) {
			return err
		}
		return err
	})

	// 记录不存在，创建
	if errors.Is(err, provider.ErrRecordNotFound) {
		// 切割rr domain
		rr, domain, err := utils.ParseDomain(subDomain)
		if err != nil {
			return err
		}

		//创建记录
		err = utils.DoWithDefaultRetry(ctx, func() error {
			_, createErr := p.operator.Create(ctx, &provider.Record{
				Type:       record.IPVersion.RecordType(),
				RR:         rr,
				DomainName: domain,
				Value:      currentAddr.String(),
				TTL:        record.TTL,
			})
			return createErr
		})

		if err == nil {

			logger.Info("DNS记录已创建", "subDomain", subDomain, "currentAddr", currentAddr)
			// 创建新记录成功发送 webhook 通知
			p.sendNotification(&webhook.WebhookData{
				Domain:   subDomain,
				OldAddr:  oldAddr.String(),
				NewAddr:  currentAddr.String(),
				Provider: p.provider.Provider,
				State:    "创建记录成功",
				Date:     time.Now().Format("2006-01-02 15:04:05"),
			})
		}
		return err
	}

	// 其他错误
	if err != nil {
		return err
	}
	//全部都更新成功才发送webhook
	hasUpdated := false
	// 记录dns api返回的IP地址
	resOldAddr := ""
	//记录存在，更新
	for _, resRecord := range resRecords {
		//DNS服务商返回的和本地当前IP地址相同，跳过更新
		if resRecord.Value == currentAddr.String() {
			logger.Info("云端解析记录未改变，无需更新", "subDomain", subDomain, "currentAddr", currentAddr)
			continue
		}
		// 记录旧IP地址，用于发送webhook
		resOldAddr = resRecord.Value
		//拷贝dns服务商返回的记录
		reqRecord := resRecord
		//赋值新IP地址
		reqRecord.Value = currentAddr.String()

		// 带重试的dns更新请求
		err := utils.DoWithDefaultRetry(ctx, func() error {
			return p.operator.Update(ctx, &reqRecord)
		})
		if err != nil {
			return fmt.Errorf("更新记录失败: %w", err)
		}
		logger.Info("DNS记录已更新", "subDomain", subDomain, "oldAddr", resRecord.Value, "newAddr", currentAddr)

		//所以记录都更新成功才发送webhook
		// 有些dns服务商相同记录可以有多条，比如：阿里云
		hasUpdated = true

	}
	if hasUpdated {
		//更新 IP 成功发送 webhook 通知
		p.sendNotification(&webhook.WebhookData{
			Domain:   subDomain,
			OldAddr:  resOldAddr,
			NewAddr:  currentAddr.String(),
			Provider: p.provider.Provider,
			State:    "更新记录成功",
			Date:     time.Now().Format("2006-01-02 15:04:05"),
		})
	}
	return nil
}

// sendNotification 封装安全的异步 Webhook 通知发送
// 网络超时由 webhook 控制
func (p *Provider) sendNotification(data *webhook.WebhookData) {
	if p.notifier == nil {
		return
	}

	// 开启协程异步发送，防止 Webhook 的网络延迟阻塞 DDNS 轮询
	go func() {
		if err := p.notifier.Send(data); err != nil {
			slog.Error("发送 Webhook 通知失败", "subDomain", data.Domain, "err", err)
		}
	}()
}

// logger 返回一个带有 provider 和 record 字段的 slog.Logger 实例，用于记录日志
func (p *Provider) logger(record string) *slog.Logger {
	return slog.With(
		"provider", p.provider.Name,
		"record", record,
	)
}
