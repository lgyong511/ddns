package engine

import (
	"context"
	"ddns/pkg/config"
	"ddns/pkg/provider"
	"ddns/pkg/provider/aliyun"
	"ddns/pkg/provider/huawei"
	"ddns/pkg/provider/tencent"
	"fmt"
	"log/slog"
	"sync"
)

// Operator 域名解析记录操作接口，组合了 CRUD 所有操作
type Operator interface {
	provider.Getter
	provider.Creator
	provider.Updater
	provider.Deleter
}

// NewOperator 根据服务商类型创建对应的 Operator 实例
func NewOperator(provider, accessKeyId, accessKeySecret string) (Operator, error) {
	switch provider {
	case "aliyun":
		return aliyun.NewAliyun(accessKeyId, accessKeySecret), nil
	case "tencent":
		return tencent.NewTencent(accessKeyId, accessKeySecret), nil
	case "huawei":
		return huawei.NewHuawei(accessKeyId, accessKeySecret), nil
	default:
		return nil, fmt.Errorf("不支持的DNS运营商：%v", provider)
	}
}

// Engine 代表整个动态域名解析引擎，负责管理配置和启动各个服务商的同步任务
type Engine struct {
	// 配置管理器
	cfgManager *config.Manager
}

// NewEngine 创建一个新的 Engine 实例
func NewEngine(cfgManager *config.Manager) *Engine {
	return &Engine{
		cfgManager: cfgManager,
	}
}

// Start 启动整个动态域名解析引擎，监听配置文件变化并热重载
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
			slog.Error("Engine启动失败！在获取配置文件时报错！err：", "err", err)
			//如果配置文件获取错误，要么收到ctx关闭信号退出程序
			//要么触发配置文件重载，重启启动
			//否则程序将组赛在此select中，直到任意通道有信号
			select {
			case <-ctx.Done():
				cancel()
				return
			case <-reloadChan:
				cancel()
				continue
			}
		}

		//依次启动Provider
		for _, provider := range cfg.Providers {
			p, err := NewProvider(&provider)
			if err != nil {
				slog.Error("初始化服务商失败，跳过该服务商", "provider", provider.Name, "err", err)
				continue
			}
			wg.Add(1)
			go func(provider *Provider) {
				defer wg.Done()
				provider.Start(pctx)

			}(p)
			slog.Info("provider 已启动", "provider", provider.Name)
		}

		//没有defaut 会堵塞在select里面，直到任意分支有信号
		select {
		case <-ctx.Done(): //处理上级ctx关闭信号
			//发送关闭信号
			cancel()
			//等待所有协程关闭
			wg.Wait()
			slog.Info("Engine 已退出")
			//退出循环，退出本函数
			return
		case <-reloadChan: //处理重置信号
			slog.Info("检测到配置变更，开始热重载")
			//发送关闭信号
			cancel()
			//等待所有协程关闭
			wg.Wait()
			//不return，因为是死循环，进入下一个循环。
		}
	}
}
