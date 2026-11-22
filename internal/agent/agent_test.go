package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"go-agent/internal/config"
	"go-agent/internal/mcp"
	"go-agent/internal/plan"
)

// newTestPlanState 构造可注入 stdin/stdout 的 plan.State（TTY 显式指定）。
func newTestPlanState(autoApprove, tty bool, stdin string) *plan.State {
	return &plan.State{
		AutoApprove: autoApprove,
		TTY:         tty,
		Stdin:       strings.NewReader(stdin),
		Stdout:      &bytes.Buffer{},
	}
}

// TestDispatchTool：本地工具路由 + 未知工具返回 Error 字符串（MCP 路由由 mcp 包测试覆盖，
// plan 态只读边界由 TestPlanModeRestrictions 覆盖）。
func TestDispatchTool(t *testing.T) {
	ps := newTestPlanState(false, true, "")
	if got := dispatchTool("no_such_tool", `{}`, nil, time.Second, ps); got != "Error: Unknown tool 'no_such_tool'" {
		t.Errorf("未知工具 = %q", got)
	}
	if got := dispatchTool("bash", `{"command":"echo routed"}`, nil, 10*time.Second, ps); got != "routed\n" {
		t.Errorf("bash 路由 = %q", got)
	}
	if got := dispatchTool("write", `{"path":"`+t.TempDir()+`/d.txt","content":"x"}`, nil, time.Second, ps); !strings.HasPrefix(got, "Successfully wrote") {
		t.Errorf("write 路由 = %q", got)
	}
	if got := dispatchTool("bash", `{bad json`, nil, time.Second, ps); !strings.HasPrefix(got, "Error: invalid tool arguments") {
		t.Errorf("坏参数 = %q", got)
	}
	// enter_plan 经 dispatch 进入计划态
	if got := dispatchTool("enter_plan", `{}`, nil, time.Second, ps); !strings.Contains(got, "You are in Plan mode") {
		t.Errorf("dispatch enter_plan = %q", got)
	}
	if !ps.Active {
		t.Error("enter_plan 后应处于 plan 态")
	}
}

// TestPlanModeRestrictions：plan 态只读边界。write/edit/bash/MCP 被禁且错误消息指明出路；
// read/glob/grep 放行。
func TestPlanModeRestrictions(t *testing.T) {
	ps := newTestPlanState(false, true, "")
	ps.Active = true
	registry := map[string]mcp.Tool{"srv__t": {Name: "t", Registered: "srv__t"}}
	blocked := []struct{ name, args string }{
		{"write", `{"path":"/tmp/x","content":"y"}`},
		{"edit", `{"path":"/tmp/x","old_string":"a","new_string":"b"}`},
		{"bash", `{"command":"echo hi"}`},
		{"srv__t", `{}`},
	}
	for _, tc := range blocked {
		got := dispatchTool(tc.name, tc.args, registry, 0, ps)
		if !strings.HasPrefix(got, "Error: ") || !strings.Contains(got, "plan mode") || !strings.Contains(got, "submit_plan") {
			t.Errorf("plan 态调用 %s 应被拦截并指明出路，实际: %q", tc.name, got)
		}
	}
	if got := dispatchTool("read", `{"path":"agent.go","limit":1}`, registry, 10*time.Second, ps); strings.HasPrefix(got, "Error:") {
		t.Errorf("plan 态 read 不应被拦截: %q", got)
	}
	if got := dispatchTool("glob", `{"pattern":"*.go"}`, registry, 10*time.Second, ps); strings.HasPrefix(got, "Error:") {
		t.Errorf("plan 态 glob 不应被拦截: %q", got)
	}
	if got := dispatchTool("grep", `{"pattern":"package","path":"."}`, registry, 10*time.Second, ps); strings.HasPrefix(got, "Error:") {
		t.Errorf("plan 态 grep 不应被拦截: %q", got)
	}
}

// bodyCollector 线程安全地收集请求体：handler goroutine 写、测试 goroutine 读，必须互斥。
type bodyCollector struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (c *bodyCollector) add(body map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies = append(c.bodies, body)
}

func (c *bodyCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func (c *bodyCollector) at(i int) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bodies[i]
}

// fakeResponsesServer 启动假 Responses API：前 len(responses)-1 轮返回 responses[i]，
// 之后重复返回最后一个；收集每个请求体，返回 (收集器, 指向该服务的测试客户端)。
func fakeResponsesServer(t *testing.T, responses []string) (*bodyCollector, openai.Client) {
	t.Helper()
	c := &bodyCollector{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("请求体不是 JSON: %v", err)
		}
		c.add(body)
		w.Header().Set("Content-Type", "application/json")
		i := c.count() - 1
		if i >= len(responses) {
			i = len(responses) - 1
		}
		io.WriteString(w, responses[i])
	}))
	t.Cleanup(srv.Close)
	return c, openai.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(srv.URL))
}

const respFunctionCall = `{"id":"resp_1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":"{\"command\":\"echo hi\"}","status":"completed"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`

const respFinalText = `{"id":"resp_2","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"all done","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`

// TestRunLoop：首轮 function_call 触发工具执行，次轮纯文本结束。验证 instructions 仅首轮发送、
// 次轮带 previous_response_id 指向上一轮 id，且结束时写出 memory。
func TestRunLoop(t *testing.T) {
	t.Chdir(t.TempDir())
	collector, client := fakeResponsesServer(t, []string{respFunctionCall, respFinalText})

	if got := Run("say hi", Options{
		Client:  client,
		MaxIter: 3,
		Timeout: 10 * time.Second,
		Plan:    plan.NewState(false),
	}); got != "all done" {
		t.Errorf("Run 最终回答 = %q, want %q", got, "all done")
	}
	if n := collector.count(); n != 2 {
		t.Fatalf("请求次数 = %d, want 2", n)
	}
	if _, ok := collector.at(0)["instructions"].(string); !ok {
		t.Errorf("首轮应携带 instructions，实际: %v", collector.at(0))
	}
	if _, ok := collector.at(1)["instructions"]; ok {
		t.Errorf("次轮不应重复发送 instructions: %v", collector.at(1))
	}
	if got, _ := collector.at(1)["previous_response_id"].(string); got != "resp_1" {
		t.Errorf("次轮 previous_response_id = %q, want resp_1", got)
	}
	mem, err := os.ReadFile(config.MemoryPath)
	if err != nil {
		t.Fatalf("应写出 %s: %v", config.MemoryPath, err)
	}
	for _, want := range []string{"say hi", "all done"} {
		if !strings.Contains(string(mem), want) {
			t.Errorf("memory 缺少 %q，实际: %q", want, mem)
		}
	}
}

// TestRunMaxIterations：模型一直请求工具时，达到 --max-iter 即返回 "Max iterations reached"。
func TestRunMaxIterations(t *testing.T) {
	t.Chdir(t.TempDir())
	collector, client := fakeResponsesServer(t, []string{respFunctionCall})

	if got := Run("loop", Options{
		Client:  client,
		MaxIter: 2,
		Timeout: 10 * time.Second,
		Plan:    plan.NewState(false),
	}); got != "Max iterations reached" {
		t.Errorf("Run 超限回答 = %q, want %q", got, "Max iterations reached")
	}
	if n := collector.count(); n != 2 {
		t.Errorf("请求次数 = %d, want 2（等于 max-iter）", n)
	}
}
