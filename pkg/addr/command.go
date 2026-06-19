package addr

import (
	"context"
	"net/netip"
)

// Command 获取IP地址，通过系统命令获取IP地址
// 支持linux、windows、macOS操作系统
type Command struct {
	// executor 执行系统命令的工具
	executor *Execute
}

// NewCommand 创建一个新的Command实例
func NewCommand(cmd string) *Command {
	return &Command{
		executor: NewExecute(cmd),
	}
}

// Fetch 获取IP地址
// 返回netip.Addr切片或者error
func (c *Command) Fetch(ctx context.Context) ([]netip.Addr, error) {
	output, err := c.executor.Execute(ctx)
	if err != nil {
		return nil, err
	}
	// 解析命令输出，提取IP地址
	return extractFromString(string(output))
}
