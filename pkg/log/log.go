package log

import (
	"io"
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
)

// 日志配置

// InitLog 初始化日志配置
// 全部使用预设值，不能自定义。
func InitLog() {

	output := io.Writer(os.Stdout)

	var handler slog.Handler

	handler = tint.NewHandler(output, &tint.Options{
		Level:      slog.LevelInfo,
		TimeFormat: "2006-01-02 15:04:05",
	})

	slog.SetDefault(slog.New(handler))
}
