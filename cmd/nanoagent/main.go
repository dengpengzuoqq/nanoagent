// nanoAgent Go 版入口：CLI flags、MCP server 启动、agent 运行。
// 运行（在项目根目录，保证能找到 ./test.env 和 ./nanoagents.md）：./agent "任务"
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"go-agent/internal/agent"
	"go-agent/internal/config"
	"go-agent/internal/mcp"
	"go-agent/internal/plan"
)

func main() {
	maxIter := flag.Int("max-iter", 20, "max agent loop iterations")
	timeoutSec := flag.Int("timeout", 30, "tool execution timeout in seconds")
	planMode := flag.Bool("plan", false, "start in plan mode (read-only; the model drafts a plan and asks for approval)")
	autoApprove := flag.Bool("auto-approve", false, "auto-approve plan submissions (required for non-interactive runs that use plan mode)")
	flag.Parse()
	task := strings.Join(flag.Args(), " ")
	if task == "" {
		fmt.Println(`Usage: agent [--plan] [--auto-approve] [--max-iter N] [--timeout S] "task"
Features: Memory, Rules, Skills, MCP, Plan mode`)
		os.Exit(1)
	}
	if config.Config.APIKey == "" {
		config.Logger.Error("缺少 OPENAI_API_KEY 配置（检查 ./test.env）")
		os.Exit(1)
	}
	timeout := time.Duration(*timeoutSec) * time.Second

	// 计划模式状态：--plan 入口①（启动即 plan 态）；入口②是模型调用 enter_plan 工具
	ps := plan.NewState(*autoApprove)
	if *planMode {
		ps.Active = true
	}

	// 启动 MCP server（标准配置格式；启动失败的服务跳过，不中断整体运行）
	var mcpTools []mcp.Tool
	var mcpClients []*mcp.Client
	servers := mcp.LoadConfig(config.MCPConfigPath)
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cfg := servers[name]
		if cfg.Disabled {
			continue
		}
		c, tools, err := mcp.Start(name, cfg, timeout)
		if err != nil {
			config.Logger.Warn("MCP server 启动失败，跳过", "server", name, "err", err)
			continue
		}
		mcpClients = append(mcpClients, c)
		mcpTools = append(mcpTools, tools...)
	}
	if len(mcpTools) > 0 {
		fmt.Printf("[MCP] Loaded %d MCP tools\n", len(mcpTools))
	}
	defer func() {
		for _, c := range mcpClients {
			mcp.Shutdown(c)
		}
	}()

	fmt.Println(agent.Run(task, agent.Options{
		Client:   config.Client,
		MaxIter:  *maxIter,
		Timeout:  timeout,
		MCPTools: mcpTools,
		Plan:     ps,
	}))
}
