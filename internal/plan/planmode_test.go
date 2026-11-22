package plan

import (
	"bytes"
	"strings"
	"testing"
)

// newTestState 构造可注入 stdin/stdout 的 State（TTY 显式指定）。
func newTestState(autoApprove, tty bool, stdin string) *State {
	return &State{
		AutoApprove: autoApprove,
		TTY:         tty,
		Stdin:       strings.NewReader(stdin),
		Stdout:      &bytes.Buffer{},
	}
}

// TestPlanModeGuards：模式守卫。EnterPlan 重复进入报错；SubmitPlan 仅 plan 态可用；空计划报错。
func TestPlanModeGuards(t *testing.T) {
	ps := newTestState(false, true, "y\n")
	if got := ps.SubmitPlan("any plan"); !strings.HasPrefix(got, "Error: submit_plan can only be used in plan mode") {
		t.Errorf("非 plan 态 SubmitPlan = %q", got)
	}
	if got := ps.EnterPlan(); !strings.Contains(got, "You are in Plan mode") {
		t.Errorf("首次 EnterPlan = %q", got)
	}
	if got := ps.EnterPlan(); got != "Error: already in plan mode" {
		t.Errorf("重复 EnterPlan = %q", got)
	}
	if got := ps.SubmitPlan("  \n "); got != "Error: plan cannot be empty" {
		t.Errorf("空计划 SubmitPlan = %q", got)
	}
}

// TestPlanModeApproval：批准三路径。TTY y 批准（切回执行态）；n+反馈 拒绝（留在 plan 态）；
// --auto-approve 直接通过。
func TestPlanModeApproval(t *testing.T) {
	// TTY + y → 批准，模式退出
	ps := newTestState(false, true, "y\n")
	ps.Active = true
	got := ps.SubmitPlan("step 1\nstep 2")
	if ps.Active {
		t.Error("批准后应退出 plan 态")
	}
	if !strings.Contains(got, "NO LONGER in Plan mode") {
		t.Errorf("批准结果缺少 leave reminder: %q", got)
	}
	// 计划全文应已打印给用户
	out := ps.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "step 1") || !strings.Contains(out, "PLAN") {
		t.Errorf("批准输出未展示计划全文: %q", out)
	}

	// TTY + n 带反馈 → 拒绝，留在 plan 态，反馈回传
	ps = newTestState(false, true, "n focus on tests first\n")
	ps.Active = true
	got = ps.SubmitPlan("step 1")
	if !ps.Active {
		t.Error("拒绝后应留在 plan 态")
	}
	if !strings.Contains(got, "focus on tests first") || !strings.Contains(got, "in Plan mode") {
		t.Errorf("拒绝结果缺少反馈与 plan reminder: %q", got)
	}

	// 裸 n → 通用反馈
	ps = newTestState(false, true, "n\n")
	ps.Active = true
	got = ps.SubmitPlan("step 1")
	if !ps.Active || !strings.Contains(got, "revise") {
		t.Errorf("裸 n 拒绝处理异常: Active=%v got=%q", ps.Active, got)
	}

	// "no" / "Nope, ..."：整句原样反馈（不能被当成 n 前缀吃掉首字母）
	for _, input := range []string{"no\n", "Nope, add tests first\n"} {
		ps = newTestState(false, true, input)
		ps.Active = true
		got = ps.SubmitPlan("step 1")
		if want := strings.TrimSpace(input); !ps.Active || !strings.Contains(got, want) {
			t.Errorf("输入 %q 反馈回传异常: Active=%v got=%q", want, ps.Active, got)
		}
	}

	// --auto-approve → 直接通过
	ps = newTestState(true, false, "")
	ps.Active = true
	got = ps.SubmitPlan("step 1")
	if ps.Active {
		t.Error("auto-approve 后应退出 plan 态")
	}
	if !strings.Contains(got, "NO LONGER in Plan mode") {
		t.Errorf("auto-approve 结果异常: %q", got)
	}
}

// TestPlanModeNonTTYFailClosed：非 TTY 且未传 --auto-approve 时 fail-closed，
// 置 FatalErr 终止运行，不进入批准循环。
func TestPlanModeNonTTYFailClosed(t *testing.T) {
	ps := newTestState(false, false, "")
	ps.Active = true
	got := ps.SubmitPlan("step 1")
	if !strings.HasPrefix(got, "Error: plan approval requires an interactive terminal") {
		t.Errorf("非 TTY 提交 = %q", got)
	}
	if ps.FatalErr == "" {
		t.Error("非 TTY 未传 --auto-approve 应置 FatalErr 使运行终止")
	}
	if !strings.Contains(ps.FatalErr, "--auto-approve") {
		t.Errorf("FatalErr 应提示 --auto-approve: %q", ps.FatalErr)
	}
}
