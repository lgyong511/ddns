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

type RecordState struct {
	Name string
	Addr netip.Addr
}

type Provider struct {
	// 服务商配置
	provider *config.Provider
	//服务商CRUD接口
	operator Operator
}

func NewProvider(provider *config.Provider) *Provider {
	return &Provider{
		provider: provider,
		operator: NewOperator(provider.Provider, provider.KeyID, provider.KeySecret),
	}
}

func (p *Provider) Start(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	// 把所有记录转成map缓存起来，key是名称
	recordCache := make(map[string]config.Record)

	//启动所有记录的获取IP地址
	recordState := make(chan RecordState, len(p.provider.Records))
	var wgRecord sync.WaitGroup
	for _, record := range p.provider.Records {
		//把所有记录写入缓存map
		recordCache[record.Name] = record
		wgRecord.Add(1)
		go func() {
			defer wgRecord.Done()
			ticker := time.NewTicker(record.Interval * time.Second)
			defer ticker.Stop()

			p.getAddr(ctx, &record, recordState)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					p.getAddr(ctx, &record, recordState)
				}
			}

		}()
		fmt.Printf("%v，启动成功！\n", record.Name)
	}

	// 记录更新缓存
	type AddrInfo struct {
		//同步时的IP地址
		Addr netip.Addr
		//上传与DNS服务商同步的时间
		lastSyncAt time.Time
	}
	//使用map存储，方便检索，key是记录名称
	addrState := make(map[string]AddrInfo)

	//声明强制DNS服务商API同步时间
	forceInterval := 30 * time.Minute

	for {
		select {
		case <-ctx.Done():
			fmt.Println("收到退出信号，provider已退出，名字：", p.provider.Name)
			wgRecord.Wait()
			close(recordState)
			fmt.Println("所有 Record 已全部退出，Provider 彻底安全关闭。")
			return

		case state, ok := <-recordState: //获取记录IP地址
			if !ok {
				return
			}

			record, exists := recordCache[state.Name]
			if !exists {
				//没有检索到跳过进入下一个循环周期
				continue
			}
			//读取本地缓存信息，判断是否需要创建或更新
			addrInfo, ok := addrState[state.Name]
			needUpdate := false
			if !ok {
				//没有缓存
				needUpdate = true
			} else if state.Addr != addrInfo.Addr {
				// 缓存的地址和获取的地址不相同
				needUpdate = true
				fmt.Printf("【IP变化】记录 [%s] 变动: %s -> %s\n", state.Name, addrInfo.Addr.String(), state.Addr.String())
			} else if time.Since(addrInfo.lastSyncAt) >= forceInterval {
				needUpdate = true
				fmt.Printf("【例行校对】记录 [%s] 的 IP 已有一段时间未与云端对齐，触发强制同步...\n", state.Name)
			}
			if !needUpdate {
				continue
			}

			//循环处理所有子域名
			for _, subDomin := range record.SubDomains {
				//调用DNS服务商API，查询记录是否存在
				resRecords, err := p.operator.GetSub(ctx, subDomin, record.IPVersion)
				if err != nil {
					//域名记录不存在，新建域名记录
					if errors.Is(err, provider.ErrRecordNotFound) {
						fmt.Println("记录不存在，开始创建记录！", subDomin)
						rr, domin, err := utils.ParseDomain(subDomin)
						if err != nil {
							fmt.Printf("子域名解析失败！子域名：%s，err: %v\n", subDomin, err)
							continue
						}
						reqRecord := provider.Record{
							Type:       record.IPVersion.RecordType(),
							Value:      state.Addr.String(),
							TTL:        record.TTL,
							DomainName: domin,
							RR:         rr,
						}
						_, err = p.operator.Create(ctx, &reqRecord)
						if err != nil {
							fmt.Printf("创建子域名记录失败！，子域名：%s，err: %v\n", subDomin, err)
							continue
						}

						fmt.Println("创建域名记录成功！")
						continue

					} else {
						fmt.Printf("获取子域名记录失败！域名：%s,err: %v\n", subDomin, err)
						continue
					}
				}

				//记录存在，比对IP是否改变
				for _, resRecord := range resRecords {
					if resRecord.Value != state.Addr.String() {
						reqRecord := resRecord
						reqRecord.Value = state.Addr.String()
						err := p.operator.Update(ctx, &reqRecord)
						if err != nil {
							fmt.Printf("更新记录失败！记录：%v，err: %v\n", resRecord, err)
							continue
						} else {
							fmt.Println("更新记录成功！记录：", reqRecord)
						}
					}
				}
			}
			//缓存记录更新时间
			addrState[record.Name] = AddrInfo{
				Addr:       state.Addr,
				lastSyncAt: time.Now(),
			}
		}
	}
}

// 启动协程获取经过版本过滤、规则筛选后的IP地址
func (p *Provider) getAddr(ctx context.Context, record *config.Record, recordState chan<- RecordState) {
	//获取IP地址
	addrs, err := addr.NewFetcher(record.GetType, record.GetValue).Fetch(ctx)
	if err != nil {
		fmt.Println("获取IP地址失败！！，record已退出，名字：", record.Name)
		return
	}
	//构建版本过滤
	versionFilter := addr.NewFilter(record.IPVersion)
	if versionFilter == nil {
		fmt.Println("IP地址版本错误！！！version：", record.IPVersion)
		return
	}
	//执行过滤
	addrs = addr.FilterAddrs(addrs, versionFilter, addr.IsPublic)
	// 构建策略并执行过滤
	addr := addr.NewSelector(record.Rule).Select(addrs)
	if !addr.IsValid() {
		fmt.Println("获取的IP地址无效！！！地址：", addr.String())
		return
	}

	select {
	case <-ctx.Done():
		return
	case recordState <- RecordState{Name: record.Name, Addr: addr}:
	}
}
