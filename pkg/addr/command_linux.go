//go:build linux

package addr

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Execute 执行系统命令
type Execute struct {
	Command string
}

// NewExecute 创建一个新的Execute实例
func NewExecute(command string) *Execute {
	return &Execute{
		Command: command,
	}
}

// Execute 执行系统命令，返回命令输出的字节切片或者error
func (e *Execute) Execute(ctx context.Context) ([]byte, error) {
	if e.Command == "" {
		return nil, fmt.Errorf("Execute：请提供Linux系统命令，如：ip addr")
	}
	// 设置一个超时时间，防止参数没有设置超时和命令执行时间过长
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "sh", "-c", e.Command).Output()
}
