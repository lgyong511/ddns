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

const (
	cloudCleanupShutdownTimeout = 5 * time.Second
	httpShutdownTimeout         = 5 * time.Second
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

	path, explicit, err := config.ResolvePath(*configPath, exeDir)
	if err != nil {
		slog.Error("无法解析配置文件路径", "error", err)
		os.Exit(1)
	}
	if explicit {
		if _, err := os.Stat(path); err != nil {
			slog.Error("显式指定的配置文件不可用", "config", path, "error", err)
			os.Exit(1)
		}
	} else if err := config.PrepareDefaultFile(path, filepath.Join(exeDir, "conf.yaml")); err != nil {
		slog.Error("无法初始化默认配置文件", "config", path, "error", err)
		os.Exit(1)
	}

	// 加载配置文件
	configManager := config.NewManager()
	defer configManager.Close()
	if err := configManager.Load(path); err != nil {
		slog.Error("配置文件加载或校验失败，程序退出", "error", err)
		os.Exit(1)

	}

	ddnsEngine := engine.NewEngine(configManager)

	// 监听操作系统停止信号，ctr+c
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *enableWeb {
		webServer, err := web.New(web.Options{
			ConfigPath:    path,
			Reloader:      configManager,
			ConfigStore:   configManager,
			ConfigChanges: configManager,
			Logs:          log.DefaultBuffer,
			CloudOperatorFactory: func(p config.Provider) (web.CloudOperator, error) {
				return engine.NewOperator(p.Provider, p.KeyID, p.KeySecret)
			},
		})
		if err != nil {
			slog.Error("Web 控制台初始化失败", "error", err)
			os.Exit(1)
		}
		engineDone := make(chan struct{})
		go func() {
			defer close(engineDone)
			ddnsEngine.Start(ctx)
		}()

		listenAddr := ":" + strings.TrimPrefix(strings.TrimSpace(*listenPort), ":")
		server := &http.Server{Addr: listenAddr, Handler: webServer.Handler(), ReadHeaderTimeout: 5 * time.Second}
		serveDone := make(chan error, 1)
		go func() {
			serveDone <- server.ListenAndServe()
		}()

		slog.Info("DDNS Web 控制台已启动", "addr", listenAddr, "config", path)
		var serveErr error
		select {
		case serveErr = <-serveDone:
			stop()
		case <-ctx.Done():
			slog.Info("开始关闭 Web 控制台")
		}

		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), cloudCleanupShutdownTimeout)
		if err := webServer.Close(cleanupCtx); err != nil {
			slog.Warn("等待云端清理任务退出失败", "error", err)
		}
		cleanupCancel()

		httpShutdownCtx, httpShutdownCancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		if err := server.Shutdown(httpShutdownCtx); err != nil && err != http.ErrServerClosed {
			slog.Warn("Web 控制台优雅关闭超时，强制关闭连接", "error", err)
			if closeErr := server.Close(); closeErr != nil {
				slog.Warn("强制关闭 Web 控制台失败", "error", closeErr)
			}
		}
		httpShutdownCancel()

		if serveErr == nil {
			serveErr = <-serveDone
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			slog.Error("Web 控制台异常退出", "error", serveErr)
		}
		slog.Info("DDNS Web 控制台已退出")
		<-engineDone
	} else {
		ddnsEngine.Start(ctx)
	}

	slog.Info("程序已退出！！！")
}

// resolveConfigPath 保留给命令行路径测试和旧调用方使用。
func resolveConfigPath(path string, exeDir string) (string, error) {
	resolved, _, err := config.ResolvePath(path, exeDir)
	return resolved, err
}

func executableDir() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exePath), nil
}
