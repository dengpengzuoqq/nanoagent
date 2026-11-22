package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go-agent/internal/config"
	"go-agent/internal/plan"
	"go-agent/internal/testutil"
)

const respFunctionCall = `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":"{\"command\":\"echo hi\"}","status":"completed"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`

const respFinalText = `{"id":"resp_2","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"all done","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`

const respSubmitPlan = `{"id":"resp_sp","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"function_call","id":"fc_sp","call_id":"call_sp","name":"submit_plan","arguments":"{\"plan\":\"step 1\"}","status":"completed"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`

// TestRunLoop：首轮 function_call 触发工具执行，次轮纯文本结束。验证 instructions 仅首轮发送、
// 次轮带 previous_response_id 指向上一轮 id，进度与工具结果经注入的 Out/Err 输出。
// memory 写入是 main 的职责（AppendMemory 另有单测），Run 只返回最终回答。
func TestRunLoop(t *testing.T) {
	rt := config.Load(t.TempDir())
	collector, client := testutil.ResponsesServer(t, []string{respFunctionCall, respFinalText})
	rt.Client = client // Runtime 的 client 可整体替换：这就是 config seam 的第二个 adapter
	out, errW := &bytes.Buffer{}, &bytes.Buffer{}

	got := Run("say hi", Options{
		Runtime: rt,
		MaxIter: 3,
		Timeout: 10 * time.Second,
		Plan:    plan.NewState(false),
		Out:     out,
		Err:     errW,
	})
	if got != "all done" {
		t.Errorf("Run 最终回答 = %q, want %q", got, "all done")
	}
	if n := collector.Count(); n != 2 {
		t.Fatalf("请求次数 = %d, want 2", n)
	}
	if _, ok := collector.At(0)["instructions"].(string); !ok {
		t.Errorf("首轮应携带 instructions，实际: %v", collector.At(0))
	}
	if _, ok := collector.At(1)["instructions"]; ok {
		t.Errorf("次轮不应重复发送 instructions: %v", collector.At(1))
	}
	if got, _ := collector.At(1)["previous_response_id"].(string); got != "resp_1" {
		t.Errorf("次轮 previous_response_id = %q, want resp_1", got)
	}
	if !strings.Contains(out.String(), `[Tool-call_1] bash(`) {
		t.Errorf("Out 应包含工具调用进度，实际: %q", out.String())
	}
	if !strings.Contains(errW.String(), "[Result-call_1]") || !strings.Contains(errW.String(), "hi") {
		t.Errorf("Err 应包含工具结果原文，实际: %q", errW.String())
	}
}

// TestRunMaxIterations：模型一直请求工具时，达到 MaxIter 即返回 "Max iterations reached"。
func TestRunMaxIterations(t *testing.T) {
	rt := config.Load(t.TempDir())
	collector, client := testutil.ResponsesServer(t, []string{respFunctionCall})
	rt.Client = client

	got := Run("loop", Options{
		Runtime: rt,
		MaxIter: 2,
		Timeout: 10 * time.Second,
		Plan:    plan.NewState(false),
		Out:     &bytes.Buffer{},
		Err:     &bytes.Buffer{},
	})
	if got != "Max iterations reached" {
		t.Errorf("Run 超限回答 = %q, want %q", got, "Max iterations reached")
	}
	if n := collector.Count(); n != 2 {
		t.Errorf("请求次数 = %d, want 2（等于 max-iter）", n)
	}
}

// TestRunPlanFailClosed：非 TTY（go test 的 stdin）且未传 --auto-approve 时提交计划，
// Run 应立即终止并返回 Fatal 信息，不发起第二轮请求。
func TestRunPlanFailClosed(t *testing.T) {
	rt := config.Load(t.TempDir())
	collector, client := testutil.ResponsesServer(t, []string{respSubmitPlan})
	rt.Client = client

	got := Run("plan it", Options{
		Runtime: rt,
		MaxIter: 3,
		Timeout: 10 * time.Second,
		Plan:    &plan.State{Active: true, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}}, // TTY=false：确定性 fail-closed
		Out:     &bytes.Buffer{},
		Err:     &bytes.Buffer{},
	})
	if !strings.HasPrefix(got, "Error: plan approval requires an interactive terminal") {
		t.Errorf("Run fail-closed = %q", got)
	}
	if n := collector.Count(); n != 1 {
		t.Errorf("请求次数 = %d, want 1（终止后不再请求）", n)
	}
}

// respCall 构造一个返回单个 function_call 的响应 JSON。
func respCall(name, argsJSON string) string {
	return fmt.Sprintf(`{"id":"resp_x","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"function_call","id":"fc_x","call_id":"call_x","name":"%s","arguments":%q,"status":"completed"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, name, argsJSON)
}

// TestRunToolRouting：工具路由与 plan 态守卫经 Run 的公开 interface 验证
// （registry 化后包内 dispatchTool 已删除，测试不再越过 interface）。
func TestRunToolRouting(t *testing.T) {
	cases := []struct {
		name   string
		resp   string
		plan   *plan.State
		wantIn string // 第二轮请求 input 中应出现的回填文本
	}{
		{"unknown tool", respCall("no_such_tool", `{}`), plan.NewState(false), "Error: Unknown tool 'no_such_tool'"},
		{"bad arguments", respCall("bash", `{bad json`), plan.NewState(false), "Error: invalid tool arguments"},
		{"submit outside plan", respCall("submit_plan", `{"plan":"x"}`), plan.NewState(false), "Error: submit_plan can only be used in plan mode"},
		{"empty plan", respCall("submit_plan", `{"plan":"   "}`),
			&plan.State{Active: true, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}},
			"Error: plan cannot be empty"},
		{"plan blocks write", respCall("write", `{"path":"/tmp/x","content":"y"}`),
			&plan.State{Active: true, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}},
			"Error: write is blocked in plan mode"},
		{"plan allows read", respCall("read", `{"path":"agent.go","limit":1}`),
			&plan.State{Active: true, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}},
			"// Package agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := config.Load(t.TempDir())
			collector, client := testutil.ResponsesServer(t, []string{tc.resp, respFinalText})
			rt.Client = client
			got := Run("task", Options{
				Runtime: rt,
				MaxIter: 3,
				Timeout: 10 * time.Second,
				Plan:    tc.plan,
				Out:     &bytes.Buffer{},
				Err:     &bytes.Buffer{},
			})
			if got != "all done" {
				t.Fatalf("Run = %q, want %q", got, "all done")
			}
			if n := collector.Count(); n != 2 {
				t.Fatalf("请求次数 = %d, want 2", n)
			}
			input, _ := json.Marshal(collector.At(1)["input"])
			if !strings.Contains(string(input), tc.wantIn) {
				t.Errorf("第二轮回填缺少 %q，实际: %s", tc.wantIn, input)
			}
		})
	}
}
