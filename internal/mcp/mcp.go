// Package mcp 是 MCP stdio 客户端：标准 mcpServers 配置、JSON-RPC 2.0 换行分隔帧、
// tools/list 发现与 tools/call 调用。
package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"go-agent/internal/config"
)

// ServerConfig 是 .nanoagents/mcp.json 中 mcpServers 的单条配置（标准 MCP 配置格式）。
type ServerConfig struct {
	Command  string            `json:"command"`
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	Disabled bool              `json:"disabled"`
}

type configFile struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// LoadConfig 读取 .nanoagents/mcp.json；文件缺失或 JSON 损坏返回空（不中断启动）。
func LoadConfig(path string) map[string]ServerConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cf configFile
	if err := json.Unmarshal(data, &cf); err != nil {
		config.Logger.Warn("MCP 配置解析失败", "path", path, "err", err)
		return nil
	}
	return cf.MCPServers
}

// Tool 是从 MCP server 发现并注册的工具；Registered = "server__工具名" 防撞名。
type Tool struct {
	Server      *Client
	Name        string // server 内的原始工具名
	Registered  string // 对外注册名
	Description string
	Schema      map[string]any
}

// rpcError 是 JSON-RPC 2.0 错误对象。
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpcResponse 只关心需要的字段；method 非空表示通知（无应答）。
type rpcResponse struct {
	ID     *int64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
	Method string          `json:"method"`
}

// Client 是一个通过 stdio 与 MCP server 子进程通信的客户端（JSON-RPC 2.0，换行分隔帧）。
// request 全程持互斥：并发调用按序执行，串行契约由实现保证而非调用方自觉。
type Client struct {
	mu        sync.Mutex
	name      string
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	responses chan *rpcResponse
	nextID    int64
}

// Start 启动 MCP server 子进程，完成 initialize 握手与 tools/list 发现。
func Start(name string, cfg ServerConfig, timeout time.Duration) (*Client, []Tool, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	c := &Client{
		name:      name,
		cmd:       cmd,
		stdin:     stdin,
		responses: make(chan *rpcResponse, 16),
	}
	// fail 统一"清理 + 带上下文报错"，三个握手阶段共用
	fail := func(format string, args ...any) (*Client, []Tool, error) {
		Shutdown(c)
		return nil, nil, fmt.Errorf(format, args...)
	}
	// 读协程：逐行解析 JSON-RPC，通知记日志丢弃，带 id 的转发给请求方
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
		for scanner.Scan() {
			var resp rpcResponse
			if json.Unmarshal(scanner.Bytes(), &resp) != nil {
				continue
			}
			if resp.Method != "" {
				config.Logger.Debug("MCP 通知", "server", name, "method", resp.Method)
				continue
			}
			c.responses <- &resp
		}
		close(c.responses)
	}()
	// initialize 握手 + initialized 通知 + tools/list 发现
	if _, err := c.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "nanoagent-go", "version": "0.1.0"},
	}, timeout); err != nil {
		return fail("MCP server %q initialize 失败: %w", name, err)
	}
	c.notify("notifications/initialized", nil)
	raw, err := c.request("tools/list", map[string]any{}, timeout)
	if err != nil {
		return fail("MCP server %q tools/list 失败: %w", name, err)
	}
	var list struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return fail("MCP server %q tools/list 响应解析失败: %w", name, err)
	}
	var tools []Tool
	for _, t := range list.Tools {
		tools = append(tools, Tool{
			Server:      c,
			Name:        t.Name,
			Registered:  name + "__" + t.Name,
			Description: t.Description,
			Schema:      t.InputSchema,
		})
	}
	config.Logger.Debug("MCP server 就绪", "server", name, "tools", len(tools))
	return c, tools, nil
}

// request 发送 JSON-RPC 请求并等待同 id 响应；通知与乱序响应被跳过；超时返回错误。
// 全程持锁：同一时刻只有一个请求在途。
func (c *Client) request(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	config.Logger.Debug("MCP 请求", "server", c.name, "method", method, "id", id)
	if _, err := c.stdin.Write(append(frame, '\n')); err != nil {
		return nil, err
	}
	deadline := time.After(timeout)
	for {
		select {
		case resp, ok := <-c.responses:
			if !ok {
				return nil, fmt.Errorf("MCP server %q 已退出（可能启动失败，检查其 stderr 输出）", c.name)
			}
			if resp.ID == nil || *resp.ID != id {
				config.Logger.Debug("MCP 跳过非当前请求的响应", "server", c.name)
				continue
			}
			if resp.Error != nil {
				return nil, fmt.Errorf("JSON-RPC error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		case <-deadline:
			return nil, fmt.Errorf("MCP 请求 %s 超时（%s）", method, timeout)
		}
	}
}

// notify 发送 JSON-RPC 通知（无需应答；写失败静默，server 下次请求时会暴露问题）。
func (c *Client) notify(method string, params any) {
	frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return
	}
	c.stdin.Write(append(frame, '\n'))
}

// CallTool 调用 MCP 工具，把 content 里所有 text 项拼接为字符串返回；
// JSON-RPC 错误与 isError 结果都转为 error，错误编码由调用方统一处理。
func (c *Client) CallTool(name string, args map[string]any, timeout time.Duration) (string, error) {
	raw, err := c.request("tools/call", map[string]any{"name": name, "arguments": args}, timeout)
	if err != nil {
		return "", err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("MCP 工具 %s 响应解析失败: %w", name, err)
	}
	var texts []string
	for _, item := range result.Content {
		if item.Type == "text" {
			texts = append(texts, item.Text)
		}
	}
	joined := strings.Join(texts, "\n")
	if result.IsError {
		if joined == "" {
			joined = "tool reported an error"
		}
		return "", errors.New(joined)
	}
	return joined, nil
}

// Shutdown 关闭 stdin 并 kill 子进程。
func Shutdown(c *Client) {
	if c == nil {
		return
	}
	c.stdin.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	c.cmd.Wait()
}
