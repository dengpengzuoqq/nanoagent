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
	if d := ps.SubmitPlan("any plan"); d.Kind != DecisionError || !strings.HasPrefix(d.Result, "Error: submit_plan can only be used in plan mode") {
		t.Errorf("非 plan 态 SubmitPlan = %+v", d)
	}
	if got := ps.EnterPlan(); !strings.Contains(got, "You are in Plan mode") {
		t.Errorf("首次 EnterPlan = %q", got)
	}
	if got := ps.EnterPlan(); got != "Error: already in plan mode" {
		t.Errorf("重复 EnterPlan = %q", got)
	}
	if d := ps.SubmitPlan("  \n "); d.Result != "Error: plan cannot be empty" {
		t.Errorf("空计划 SubmitPlan = %+v", d)
	}
}

// TestPlanModeApproval：批准三路径。TTY y 批准（切回执行态）；n+反馈 拒绝（留在 plan 态）；
// --auto-approve 直接通过。
func TestPlanModeApproval(t *testing.T) {
	// TTY + y → 批准，模式退出
	ps := newTestState(false, true, "y\n")
	ps.Active = true
	d := ps.SubmitPlan("step 1\nstep 2")
	if ps.Active {
		t.Error("批准后应退出 plan 态")
	}
	if d.Kind != DecisionApprove || !strings.Contains(d.Result, "NO LONGER in Plan mode") {
		t.Errorf("批准结果异常: %+v", d)
	}
	// 计划全文应已打印给用户
	out := ps.Stdout.(*bytes.Buffer).String()
	if !strings.Contains(out, "step 1") || !strings.Contains(out, "PLAN") {
		t.Errorf("批准输出未展示计划全文: %q", out)
	}

	// TTY + n 带反馈 → 拒绝，留在 plan 态，反馈回传
	ps = newTestState(false, true, "n focus on tests first\n")
	ps.Active = true
	d = ps.SubmitPlan("step 1")
	if !ps.Active {
		t.Error("拒绝后应留在 plan 态")
	}
	if d.Kind != DecisionReject || !strings.Contains(d.Result, "focus on tests first") || !strings.Contains(d.Result, "in Plan mode") {
		t.Errorf("拒绝结果缺少反馈与 plan reminder: %+v", d)
	}

	// 裸 n → 通用反馈
	ps = newTestState(false, true, "n\n")
	ps.Active = true
	d = ps.SubmitPlan("step 1")
	if !ps.Active || d.Kind != DecisionReject || !strings.Contains(d.Result, "revise") {
		t.Errorf("裸 n 拒绝处理异常: Active=%v d=%+v", ps.Active, d)
	}

	// "no" / "Nope, ..."：整句原样反馈（不能被当成 n 前缀吃掉首字母）
	for _, input := range []string{"no\n", "Nope, add tests first\n"} {
		ps = newTestState(false, true, input)
		ps.Active = true
		d = ps.SubmitPlan("step 1")
		if want := strings.TrimSpace(input); !ps.Active || !strings.Contains(d.Result, want) {
			t.Errorf("输入 %q 反馈回传异常: Active=%v got=%q", want, ps.Active, d.Result)
		}
	}

	// --auto-approve → 直接通过
	ps = newTestState(true, false, "")
	ps.Active = true
	d = ps.SubmitPlan("step 1")
	if ps.Active {
		t.Error("auto-approve 后应退出 plan 态")
	}
	if d.Kind != DecisionApprove || !strings.Contains(d.Result, "NO LONGER in Plan mode") {
		t.Errorf("auto-approve 结果异常: %+v", d)
	}
}

// TestPlanModeNonTTYFailClosed：非 TTY 且未传 --auto-approve 时 fail-closed，
// 返回 DecisionFatal 终止运行，不进入批准循环。
func TestPlanModeNonTTYFailClosed(t *testing.T) {
	ps := newTestState(false, false, "")
	ps.Active = true
	d := ps.SubmitPlan("step 1")
	if d.Kind != DecisionFatal {
		t.Errorf("非 TTY 提交应返回 DecisionFatal: %+v", d)
	}
	if !strings.HasPrefix(d.Fatal, "Error: plan approval requires an interactive terminal") {
		t.Errorf("Fatal 信息异常: %q", d.Fatal)
	}
	if !strings.Contains(d.Fatal, "--auto-approve") {
		t.Errorf("Fatal 应提示 --auto-approve: %q", d.Fatal)
	}
}
