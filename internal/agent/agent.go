// Package agent 是 Agent 主循环：Responses API + previous_response_id 链式上下文、工具分发、
// 计划模式接线。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"go-agent/internal/config"
	"go-agent/internal/mcp"
	"go-agent/internal/plan"
	"go-agent/internal/prompt"
	"go-agent/internal/tools"
)

// dispatchTool 按名称分发工具调用（本地工具 + plan 模式工具 + MCP 注册工具）；
// 任何错误都以字符串返回，保证 agent loop 继续。ps 承载计划模式的状态与守卫。
func dispatchTool(name, argsJSON string, mcpRegistry map[string]mcp.Tool, timeout time.Duration, ps *plan.State) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("Error: invalid tool arguments: %v", err)
	}
	str := func(key string) string { s, _ := args[key].(string); return s }
	intArg := func(key string) int { n, _ := args[key].(float64); return int(n) }

	// 计划模式工具（模式守卫在 plan.State 内）
	switch name {
	case "enter_plan":
		return ps.EnterPlan()
	case "submit_plan":
		return ps.SubmitPlan(str("plan"))
	}
	// plan 态只读边界：硬禁 write/edit/bash/MCP；read/glob/grep 放行
	isMCPTool := func(n string) bool { _, ok := mcpRegistry[n]; return ok }
	if msg, blocked := ps.PlanGuard(name, isMCPTool); blocked {
		return msg
	}

	switch name {
	case "bash":
		return tools.Bash(str("command"), timeout)
	case "read":
		return tools.Read(str("path"), intArg("offset"), intArg("limit"))
	case "write":
		return tools.Write(str("path"), str("content"))
	case "edit":
		return tools.Edit(str("path"), str("old_string"), str("new_string"))
	case "glob":
		return tools.Glob(str("pattern"))
	case "grep":
		path := str("path")
		if path == "" {
			path = "."
		}
		return tools.Grep(str("pattern"), path, timeout)
	default:
		if t, ok := mcpRegistry[name]; ok {
			// MCP 工具参数原样透传（复用上方已解析的 args）
			return t.Server.CallTool(t.Name, args, timeout)
		}
		return fmt.Sprintf("Error: Unknown tool '%s'", name)
	}
}

// Options 聚合 Run 的运行参数与依赖。
type Options struct {
	Runtime  config.Runtime // 运行环境：配置、路径布局、API client
	MaxIter  int            // 最大迭代轮数
	Timeout  time.Duration  // 工具单次调用超时
	MCPTools []mcp.Tool     // 已注册的 MCP 工具
	Plan     *plan.State    // 计划模式状态
	Out      io.Writer      // 进度输出（[Tool-] 行）；nil 时用 os.Stdout
	Err      io.Writer      // 工具结果原文输出（[Result-] 行）；nil 时用 os.Stderr
}

// Run 执行一次任务：组装工具表与 system prompt，循环调用 Responses API 直到没有工具调用、
// 达到 MaxIter 或计划模式 fail-closed 终止，返回最终回答。跨任务记忆由调用方写入。
func Run(userMessage string, opts Options) string {
	rt := opts.Runtime
	client, maxIterations, timeout, ps := rt.Client, opts.MaxIter, opts.Timeout, opts.Plan
	mcpTools := opts.MCPTools
	out, errW := opts.Out, opts.Err
	if out == nil {
		out = os.Stdout
	}
	if errW == nil {
		errW = os.Stderr
	}
	config.Logger.Debug("run_agent 启动", "user_message", userMessage, "max_iterations", maxIterations)
	// 本地工具 + 计划模式工具 + MCP 工具并入工具表，注册名 -> 工具 的映射用于分发
	toolList := append([]responses.ToolUnionParam{}, tools.LocalTools...)
	toolList = append(toolList, plan.PlanTools...)
	mcpRegistry := map[string]mcp.Tool{}
	for _, t := range mcpTools {
		toolList = append(toolList, tools.FunctionTool(t.Registered, t.Description, t.Schema))
		mcpRegistry[t.Registered] = t
	}
	systemPrompt := prompt.BuildSystemPrompt(rt)
	if ps.Active {
		// --plan 入口：enter reminder 注入首轮 instructions（入口② enter_plan 经 tool result 注入）
		systemPrompt += "\n\n" + plan.PlanEnterReminder
		fmt.Fprintln(ps.Stdout, "[Plan mode] started with --plan (read-only until the plan is approved)")
	}
	// Responses API：system prompt 放 instructions，input 只放对话条目
	inputItems := responses.ResponseInputParam{
		{OfMessage: &responses.EasyInputMessageParam{
			Role:    responses.EasyInputMessageRoleUser,
			Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(userMessage)},
		}},
	}
	previousResponseID := ""
	model := rt.Model()
	finalText := ""
	reachedLimit := true // 循环跑完仍没等到"无工具调用"的回答，即为达到上限
loop:
	for i := 0; i < maxIterations; i++ {
		config.Logger.Debug("轮请求", "round", fmt.Sprintf("%d/%d", i+1, maxIterations),
			"model", model, "new_input_items", len(inputItems), "previous_response_id", previousResponseID)

		params := responses.ResponseNewParams{
			Model: shared.ResponsesModel(model),
			Input: responses.ResponseNewParamsInputUnion{OfInputItemList: inputItems},
			Tools: toolList,
			Store: openai.Bool(true),
		}
		// instructions 仅首轮发送；后续轮次由服务端按 previous_response_id 回放，前缀不变
		if previousResponseID == "" {
			params.Instructions = openai.String(systemPrompt)
		} else {
			params.PreviousResponseID = openai.String(previousResponseID)
		}
		resp, err := client.Responses.New(context.Background(), params)
		if err != nil {
			config.Logger.Error("API 请求失败", "err", err)
			return fmt.Sprintf("Error: API request failed: %v", err)
		}
		var toolCalls []responses.ResponseFunctionToolCall
		for _, item := range resp.Output {
			if item.Type == "function_call" {
				toolCalls = append(toolCalls, item.AsFunctionCall())
			}
		}
		config.Logger.Debug("响应", "id", resp.ID, "status", resp.Status, "function_calls", len(toolCalls),
			"input_tokens", resp.Usage.InputTokens, "output_tokens", resp.Usage.OutputTokens,
			"cached_tokens", resp.Usage.InputTokensDetails.CachedTokens)
		if len(toolCalls) == 0 {
			finalText = resp.OutputText()
			reachedLimit = false
			config.Logger.Debug("无工具调用，返回最终回答", "len", len(finalText))
			break
		}
		nextItems := responses.ResponseInputParam{}
		for _, call := range toolCalls {
			fmt.Fprintf(out, "[Tool-%s] %s(%s)\n", call.CallID, call.Name, call.Arguments)
			config.Logger.Debug("执行工具", "name", call.Name, "call_id", call.CallID)
			result := dispatchTool(call.Name, call.Arguments, mcpRegistry, timeout, ps)
			// 非 TTY 未传 --auto-approve：fail-closed，立即终止整个运行
			if ps.FatalErr != "" {
				finalText = ps.FatalErr
				reachedLimit = false
				break loop
			}
			// 工具结果原文直写 errW，不经 slog 转义
			fmt.Fprintf(errW, "[Result-%s] %s\n%s\n", call.CallID, call.Name, result)
			// 工具结果以 function_call_output 条目回填，call_id 必须一一对应
			nextItems = append(nextItems, responses.ResponseInputItemUnionParam{
				OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: openai.String(call.CallID),
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
						OfString: openai.String(result),
					},
				},
			})
		}
		// 下一轮只发新增工具结果；历史由服务端按 previous_response_id 回放，
		// 前缀不变以命中 KV 缓存
		inputItems = nextItems
		previousResponseID = resp.ID
	}
	if reachedLimit {
		config.Logger.Debug("达到最大迭代次数，退出循环", "max_iterations", maxIterations)
		finalText = "Max iterations reached"
	}
	return finalText
}
