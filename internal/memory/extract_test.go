package memory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"go-agent/internal/config"
	"go-agent/internal/testutil"
)

// extractResp 构造一个返回 output_text 的响应 JSON（text 为提取调用的 JSON 决策）。
func extractResp(text string) string {
	return `{"id":"e1","object":"response","created_at":1,"status":"completed","model":"m","output":[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":` + quoteJSON(text) + `,"annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
}

// quoteJSON 把任意字符串编码为 JSON 字符串字面量（含转义）。
func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Extract：ADD 动作落盘并重建索引。
func TestExtractADD(t *testing.T) {
	rt := config.Load(t.TempDir())
	_, client := testutil.ResponsesServer(t, []string{extractResp(
		`{"actions":[{"op":"ADD","id":"go-testing","category":"lesson","content":"提交前先跑 make test","tags":["testing"],"weight":4,"source":"修测试"}]}`)})
	rt.Client = client

	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	if err := Extract(rt, "修测试", "已修复", "make test 通过", now); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	s := Open(rt.Paths.MemoryDir)
	e := s.findByID("go-testing")
	if e == nil || e.Category != CatLesson || e.Weight != 4 || e.Source != "修测试" {
		t.Errorf("ADD 结果异常: %+v", e)
	}
	idx, err := os.ReadFile(rt.Paths.MemoryDir + "/MEMORY.md")
	if err != nil || !strings.Contains(string(idx), "[go-testing](go-testing.md)") {
		t.Errorf("索引未重建: %q, err=%v", idx, err)
	}
	// 提取输入应包含规则与本次任务
}

// Extract：惰性迁移——旧格式时间线改写为 ADD 后原文件删除。
func TestExtractMigration(t *testing.T) {
	rt := config.Load(t.TempDir())
	os.WriteFile(rt.Paths.MemoryPath, []byte("## 2026-11-20 15:04:05\n**Task:** 旧任务\n**Result:** 旧结果\n"), 0o644)
	_, client := testutil.ResponsesServer(t, []string{extractResp(
		`{"actions":[{"op":"ADD","id":"legacy-fact","category":"fact","content":"从旧格式迁移的事实","weight":2}]}`)})
	rt.Client = client

	if err := Extract(rt, "任务", "结果", "", time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := os.Stat(rt.Paths.MemoryPath); !os.IsNotExist(err) {
		t.Error("迁移完成后旧格式文件应删除")
	}
	s := Open(rt.Paths.MemoryDir)
	if len(s.Entries) != 1 || s.Entries[0].ID != "legacy-fact" {
		t.Errorf("迁移结果 = %+v", s.Entries)
	}
}

// Extract：服务端失败时返回 error 且 memory 保持原样（静默降级）。
func TestExtractFailureSilent(t *testing.T) {
	rt := config.Load(t.TempDir())
	s := Open(rt.Paths.MemoryDir)
	s.Entries = []Entry{sampleEntry("keep")}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(rt.Paths.MemoryDir + "/keep.md")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	rt.Client = openai.NewClient(option.WithAPIKey("k"), option.WithBaseURL(srv.URL))

	if err := Extract(rt, "t", "r", "", time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("服务端错误应返回 error")
	}
	after, _ := os.ReadFile(rt.Paths.MemoryDir + "/keep.md")
	if string(before) != string(after) {
		t.Error("提取失败时 memory 不应被修改")
	}
	if _, err := os.Stat(rt.Paths.MemoryPath); err == nil {
		t.Log("存在旧格式文件时提取失败应保留（重试）")
	}
}

// parseActions：容忍代码围栏与前后缀；坏输出报错。语义校验（如非法类别）
// 由 ApplyActions 拦截，parse 只负责结构。
func TestParseActions(t *testing.T) {
	actions, err := parseActions("好的，以下是决策：\n```json\n{\"actions\":[{\"op\":\"ADD\",\"id\":\"x\",\"category\":\"fact\",\"content\":\"c\"}]}\n```\n以上。")
	if err != nil {
		t.Fatalf("parseActions: %v", err)
	}
	if len(actions) != 1 || actions[0].ID != "x" {
		t.Errorf("actions = %+v", actions)
	}
	if _, err := parseActions("没有任何 JSON"); err == nil {
		t.Error("无 JSON 应报错")
	}
}
