package main

import (
	"context"
	"ddns/pkg/config"
	"ddns/pkg/engine"
	"ddns/pkg/log"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	// 初始化日志配置
	log.InitLog()

	// 解析命令行参数，获取配置文件路径
	configPath := flag.String("c", "", "请输入配置文件路径")
	flag.Parse()

	// 如果没有指定配置文件路径，则使用默认路径
	path, err := resolveCofigPath(*configPath)
	if err != nil {
		slog.Error("无法解析配置文件路径", "error", err)
		os.Exit(1)
	}

	// 加载配置文件
	configManager := config.NewManager()
	if err := configManager.Load(path); err != nil {
		slog.Error("配置文件加载或校验失败，程序退出", "error", err)
		panic(err)
	}

	engine := engine.NewEngine(configManager)

	// 监听操作系统停止信号，ctr+c
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	engine.Start(ctx)

	slog.Info("程序已退出！！！")
}

// resolveConfigPath 确定配置文件路径，如果用户没有指定，则使用程序所在目录的 conf.yaml
func resolveCofigPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	// 获取当前可执行文件的目录
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	exeDir := filepath.Dir(exePath)
	return filepath.Join(exeDir, "conf.yaml"), nil
}
