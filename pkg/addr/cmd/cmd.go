package cmd

import (
	"context"
	"ddns/pkg/addr"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

//使用策略模式
//通过执行命令来获取IP地址信息
// 支持的操作系统有：linux、windows、macos

const (
	timeout = 5
)

type Executor interface {
	//执行命令,返回命令执行结果
	Execute(string) ([]byte, error)
}

// Windows windows命令执行器
type Windows struct {
}

// Execute 执行命令
func (w *Windows) Execute(cmd string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "powershell", "/C", cmd).Output()
}

// Linux linux命令执行器
type Linux struct {
}

// Execute 执行命令
func (l *Linux) Execute(cmd string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "bash", "-c", cmd).Output()
}

// Macos macos命令执行器
type Darwin struct {
}

// Execute 执行命令
func (m *Darwin) Execute(cmd string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "zsh", "-c", cmd).Output()
}

// Cmd 命令执行器
type Cmd struct {
	command  string
	executor Executor
}

// NewCmd 创建命令执行器
func NewCmd(command string, executor Executor) *Cmd {
	return &Cmd{
		command:  command,
		executor: executor,
	}
}

func (c *Cmd) Get(ipType addr.IPType, rule string) (net.IP, error) {
	ips, err := c.GetAll()
	if err != nil {
		return nil, err
	}
	return addr.MatchReg(ipType, ips, rule)
}

// GetAll 获取执行命令得到的所有IP地址
func (c *Cmd) GetAll() ([]net.IP, error) {
	output, err := c.executor.Execute(c.command)
	if err != nil {
		return nil, err
	}
	ips, err := parseIPs(string(output))
	if err != nil {
		return nil, err
	}
	return ips, nil
}

func parseIPs(str string) (ips []net.IP, err error) {
	// 如果输入为空字符串，返回错误
	if str == "" {
		return nil, fmt.Errorf("empty string input")
	}
	// 按行分割
	strs := strings.Split(str, "\n")
	// 遍历每一行，提取IP地址
	for _, str := range strs {
		str = strings.TrimSpace(str)
		ip := net.ParseIP(str)
		if ip != nil && ip.IsGlobalUnicast() {
			ips = append(ips, ip)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("parse ip error")
	}
	return ips, nil
}
