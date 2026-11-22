// Package plan 实现计划模式：模型自主或 --plan 进入；plan 态只读；退出必须经用户批准
// （submit_plan），拒绝则带反馈留在 plan 态修订。
package plan

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go-agent/internal/tools"
)

// PlanEnterReminder / planLeaveReminder 是注入给模型的 synthetic reminder：
// 进入时告知只读边界与出路，退出时声明限制解除。
const PlanEnterReminder = `<system-reminder>
You are in Plan mode (read-only). Explore the codebase with read/glob/grep and draft a plan. Do not modify any files: write/edit/bash and MCP tools are blocked in this mode. When your plan is ready, call submit_plan with the complete plan text. You remain in Plan mode until the user approves your plan.
</system-reminder>`

const planLeaveReminder = `<system-reminder>
You are NO LONGER in Plan mode. The previous Plan restrictions no longer apply. Any Plan mode instructions from earlier in this conversation are no longer active. Execute the approved plan now.
</system-reminder>`

// DecisionKind 标记 submit_plan 的结果类别。
type DecisionKind int

const (
	DecisionApprove DecisionKind = iota // 批准：切回执行态
	DecisionReject                      // 拒绝：留在 plan 态，反馈随结果返回
	DecisionFatal                       // 终止整个运行（非 TTY 未批准 / 输入中断）
)

// Decision 是 submit_plan 审批的结果。非 fatal 时 Result 是回给模型的 tool result；
// fatal 时 Fatal 是终止原因，由调用方写入最终输出。
type Decision struct {
	Kind   DecisionKind
	Result string
	Fatal  string
}

// State 是计划模式的会话级状态；TTY/Stdin/Stdout 可注入以便测试。
type State struct {
	Active      bool
	AutoApprove bool      // --auto-approve：跳过人审
	TTY         bool      // stdin 是否为终端；非 TTY 且未 auto-approve 时 fail-closed
	Stdin       io.Reader // 批准输入
	Stdout      io.Writer // 计划展示与提示输出
}

// NewState 构造绑定真实 stdin/stdout 的计划模式状态。
func NewState(autoApprove bool) *State {
	return &State{
		AutoApprove: autoApprove,
		TTY:         isTTY(os.Stdin),
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
	}
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	// /dev/null 也是字符设备（重定向/管道场景的常见形态），必须排除
	if null, err := os.Stat("/dev/null"); err == nil && os.SameFile(st, null) {
		return false
	}
	return true
}

// EnterPlan 进入计划模式；已在 plan 态返回错误。
func (p *State) EnterPlan() (string, error) {
	if p.Active {
		return "", errors.New("already in plan mode")
	}
	p.Active = true
	fmt.Fprintln(p.Stdout, "[Plan mode] entered (read-only; submit your plan with submit_plan)")
	return PlanEnterReminder, nil
}

// SubmitPlan 提交计划等待用户批准（仅 plan 态可调，plan 非空由调用方保证）：
// 批准 → 切回执行态并返回 leave reminder；拒绝 → 留在 plan 态，反馈随结果返回；
// 非 TTY 未批准 → DecisionFatal，调用方据此终止整个运行。
func (p *State) SubmitPlan(plan string) Decision {
	fmt.Fprintf(p.Stdout, "\n========== PLAN ==========\n%s\n==========================\n", plan)
	d := p.askApproval()
	if d.Kind == DecisionApprove {
		p.Active = false
		fmt.Fprintln(p.Stdout, "[Plan] approved — executing")
	}
	return d
}

// askApproval 处理批准决策：--auto-approve 直接通过；非 TTY fail-closed；
// TTY 读一行：Enter/y 批准，n[ 反馈...] 拒绝，其他输入按拒绝处理。
func (p *State) askApproval() Decision {
	if p.AutoApprove {
		fmt.Fprintln(p.Stdout, "[Plan] auto-approved (--auto-approve)")
		return Decision{Kind: DecisionApprove, Result: planLeaveReminder}
	}
	if !p.TTY {
		fmt.Fprintln(p.Stdout, "[Plan] NOT approved: stdin is not a terminal")
		return Decision{Kind: DecisionFatal, Fatal: "Error: plan approval requires an interactive terminal; rerun with --auto-approve to skip approval in scripts"}
	}
	reader := bufio.NewReader(p.Stdin)
	fmt.Fprint(p.Stdout, "Approve this plan? [Enter/y=yes, n <feedback>=revise]: ")
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return Decision{Kind: DecisionFatal, Fatal: "Error: plan approval aborted: no user input"}
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	if ans == "" || ans == "y" || ans == "yes" {
		return Decision{Kind: DecisionApprove, Result: planLeaveReminder}
	}
	feedback := strings.TrimSpace(line)
	// 只剥掉独立的 "n"（"n fix tests" → "fix tests"）；"no"/"Nope, add tests" 整句保留
	if fields := strings.Fields(feedback); len(fields) > 0 && strings.EqualFold(fields[0], "n") {
		feedback = strings.Join(fields[1:], " ")
	}
	if feedback == "" {
		feedback = "Please revise the plan."
	}
	return Decision{Kind: DecisionReject, Result: fmt.Sprintf("%s\nThe user reviewed your plan and rejected it with feedback: %s\nRevise your plan and call submit_plan again.", PlanEnterReminder, feedback)}
}

// FatalError 表示计划模式 fail-closed：终止整个运行，Message 写入最终输出。
type FatalError struct{ Message string }

func (e *FatalError) Error() string { return e.Message }

// PlanTools 构造计划模式专用工具：enter_plan（进入）与 submit_plan（提交批准）。
// ReadOnly=true：plan 态可用；误用时经 DecisionError 回给模型。
func PlanTools(p *State) []tools.Tool {
	return []tools.Tool{
		{
			Name:        "enter_plan",
			Description: "Enter plan mode: switch to read-only planning. In plan mode you explore the codebase (read/glob/grep only), draft a plan, and submit it with submit_plan for user approval. Use for complex multi-step tasks",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   []string{},
			},
			ReadOnly: true,
			Execute: func(args map[string]any, timeout time.Duration) (string, error) {
				return p.EnterPlan()
			},
		},
		{
			Name:        "submit_plan",
			Description: "Submit your complete plan for user approval. Only usable in plan mode. If approved, execution begins; if rejected, you get feedback and stay in plan mode to revise",
			Schema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"plan": map[string]any{"type": "string"}},
				"required":   []string{"plan"},
			},
			ReadOnly: true,
			Execute: func(args map[string]any, timeout time.Duration) (string, error) {
				if !p.Active {
					return "", errors.New("submit_plan can only be used in plan mode (call enter_plan first, or start the agent with --plan)")
				}
				planText := tools.StrArg(args, "plan")
				if strings.TrimSpace(planText) == "" {
					return "", errors.New("plan cannot be empty")
				}
				d := p.SubmitPlan(planText)
				// Kind 是 fatal 的唯一裁决：Fatal 仅作载荷
				if d.Kind == DecisionFatal {
					return "", &FatalError{Message: d.Fatal}
				}
				return d.Result, nil
			},
		},
	}
}
