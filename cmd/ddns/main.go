package main

import (
	"context"
	"ddns/pkg/config"
	"ddns/pkg/engine"
	"ddns/pkg/log"
	"ddns/pkg/version"
	"ddns/pkg/web"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	// 初始化日志配置
	log.InitLog()

	// 解析命令行参数，获取配置文件路径
	configPath := flag.String("c", "", "请输入配置文件路径")
	enableWeb := flag.Bool("web", false, "是否启动 Web 控制台")
	listenPort := flag.String("p", "8686", "Web 控制台监听端口")
	showVersion := flag.Bool("version", false, "输出当前版本")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return
	}
	slog.Info("DDNS 程序启动", "version", version.Version)

	exeDir, err := executableDir()
	if err != nil {
		slog.Error("无法解析配置文件路径", "error", err)
		os.Exit(1)
	}

	path := ""
	if *enableWeb {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			slog.Error("无法解析用户家目录", "error", err)
			os.Exit(1)
		}
		path = filepath.Join(homeDir, ".ddns_conf.yaml")
		importCandidates := []string{*configPath, filepath.Join(exeDir, "conf.yaml")}
		if err := web.PrepareConfigFile(path, importCandidates); err != nil {
			slog.Error("无法初始化 Web 配置文件", "config", path, "error", err)
			os.Exit(1)
		}
	} else {
		var err error
		path, err = resolveConfigPath(*configPath, exeDir)
		if err != nil {
			slog.Error("无法解析配置文件路径", "error", err)
			os.Exit(1)
		}
	}

	// 加载配置文件
	configManager := config.NewManager()
	if err := configManager.Load(path); err != nil {
		slog.Error("配置文件加载或校验失败，程序退出", "error", err)
		os.Exit(1)

	}

	engine := engine.NewEngine(configManager)

	// 监听操作系统停止信号，ctr+c
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *enableWeb {
		go engine.Start(ctx)

		webServer, err := web.New(web.Options{ConfigPath: path, Reloader: configManager, Logs: log.DefaultBuffer})
		if err != nil {
			slog.Error("Web 控制台初始化失败", "error", err)
			os.Exit(1)
		}

		listenAddr := ":" + strings.TrimPrefix(strings.TrimSpace(*listenPort), ":")
		server := &http.Server{Addr: listenAddr, Handler: webServer.Handler(), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()

		slog.Info("DDNS Web 控制台已启动", "addr", listenAddr, "config", path)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Web 控制台异常退出", "error", err)
			os.Exit(1)
		}
	} else {
		engine.Start(ctx)
	}

	slog.Info("程序已退出！！！")
}

// resolveConfigPath 确定配置文件路径，如果用户没有指定，则使用程序所在目录的 conf.yaml
func resolveConfigPath(path string, exeDir string) (string, error) {
	if path != "" {
		return path, nil
	}
	return filepath.Join(exeDir, "conf.yaml"), nil
}

func executableDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exePath), nil
}
