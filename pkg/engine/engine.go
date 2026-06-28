package engine

import (
	"context"
	"ddns/pkg/config"
	"ddns/pkg/provider"
	"ddns/pkg/provider/aliyun"
	"fmt"
	"sync"
)

// Operator 域名解析记录操作接口，组合了 CRUD 所有操作
type Operator interface {
	provider.Getter
	provider.Creator
	provider.Updater
	provider.Deleter
}

func NewOperator(provider, accessKeyId, accessKeySecret string) Operator {
	switch provider {
	case "aliyun":
		return aliyun.NewAliyun(accessKeyId, accessKeySecret)
	default:
		return nil

	}

}

type Engine struct {
	// 配置管理器
	cfgManager *config.Manager
}

func NewEngine(cfgManager *config.Manager) *Engine {
	return &Engine{
		cfgManager: cfgManager,
	}
}

func (e *Engine) Start(ctx context.Context) {
	// 声明热加载通道
	reloadChan := make(chan struct{}, 1)
	// 注册配置回调函数
	e.cfgManager.RegCallback(func() {
		//发送重启信号
		select {
		case reloadChan <- struct{}{}:
		default:
		}
	})

	for {
		pctx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		cfg, err := e.cfgManager.Get()
		if err != nil {
			fmt.Println("Engine启动失败！在获取配置文件是报错！err：", err)
			select {
			case <-ctx.Done():
				cancel()
				return
			case <-reloadChan:
				cancel()
				continue
			}
		}
		for _, provider := range cfg.Providers {
			wg.Add(1)
			go NewProvider(&provider).Start(pctx, &wg)
			fmt.Printf("%v，启动成功！\n", provider.Name)
		}

		//没有defaut 会堵塞在select里面，直到任意分支有信号
		select {
		case <-ctx.Done(): //处理上级ctx关闭信号
			//发送关闭信号
			cancel()
			//等待所有协程关闭
			wg.Wait()
			//退出循环，退出本函数
			return
		case <-reloadChan: //处理重置信号
			fmt.Println("检测到配置文件变化！开始热重载！！！")
			//发送关闭信号
			cancel()
			//等待所有协程关闭
			wg.Wait()
			//不return，因为是死循环，进入下一个循环。
		}
	}
}
