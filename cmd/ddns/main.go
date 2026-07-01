package main

import (
	"context"
	"ddns/pkg/config"
	"ddns/pkg/engine"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {

	configPath := flag.String("c", "", "请输入配置文件路径")
	flag.Parse()

	path, err := resolveCofigPath(*configPath)
	if err != nil {
		panic(err)
	}

	config := config.NewManager()
	if err := config.Load(path); err != nil {
		panic(err)
	}
	engine := engine.NewEngine(config)

	// 监听操作系统停止信号，ctr+c
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	engine.Start(ctx)

	fmt.Println("程序已退出！")
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
