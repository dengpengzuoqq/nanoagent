package memory

import (
	"strings"
	"testing"
	"time"

	"go-agent/internal/config"
)

// BigramScorer：字符 bigram 重合度 × 权重加权；查询无重合不进结果。
func TestBigramScoring(t *testing.T) {
	scorer := BigramScorer{}
	entries := []Entry{
		{ID: "testing", Category: CatLesson, Content: "提交前先跑 make test", Description: "测试纪律", Tags: []string{"testing"}, Weight: 4},
		{ID: "deploy", Category: CatFact, Content: "部署用 make build", Description: "构建部署", Weight: 2},
	}
	hits := scorer.Score("测试 提交 testing", entries)
	if len(hits) == 0 || hits[0].Entry.ID != "testing" {
		t.Fatalf("检索首条 = %+v, want testing", hits)
	}
	if hits[0].Score <= hits[len(hits)-1].Score && len(hits) > 1 {
		t.Errorf("命中应按得分降序: %+v", hits)
	}
	if hits := scorer.Score("完全无关的查询词xyz", entries); len(hits) != 0 {
		t.Errorf("无重合查询不应有命中: %+v", hits)
	}
}

// SystemSection：空记忆占位语；有条目时含纪律、索引与常驻正文；预算截断。
func TestSystemSection(t *testing.T) {
	empty := SystemSection(Open(t.TempDir()))
	if !strings.Contains(empty, "Your memory is currently empty") || !strings.Contains(empty, "ONE fact per file") {
		t.Errorf("空记忆段缺少占位语或写入纪律: %q", empty)
	}

	s := Open(t.TempDir())
	s.Entries = []Entry{
		{ID: "pin", Category: CatConstraint, Content: "约束内容", Pinned: true, Weight: 1, Description: "约束"},
		{ID: "top", Category: CatLesson, Content: "高权重内容", Weight: 5, Description: "教训"},
		{ID: "low", Category: CatFact, Content: strings.Repeat("长尾", 2000), Weight: 1, Description: "长尾"},
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	section := SystemSection(s)
	for _, want := range []string{"## Index", "- [pin](pin.md)", "- [top](top.md)", "### pin (constraint", "高权重内容"} {
		if !strings.Contains(section, want) {
			t.Errorf("记忆段缺少 %q", want)
		}
	}
	if strings.Contains(section, strings.Repeat("长尾", 2000)) {
		t.Error("长尾条目不应进常驻区（靠 memory_search 检索）")
	}
}

// memory_search 工具：命中返回条目正文并强化权重（持久化）。
func TestSearchToolBump(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	e := Entry{ID: "testing", Category: CatLesson, Content: "提交前先跑 make test", Description: "测试纪律", Weight: 4,
		CreatedAt: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC), LastAccessed: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)}
	s.Entries = []Entry{e}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	tool := SearchTool(dir, BigramScorer{})
	got, err := tool.Execute(map[string]any{"query": "testing 提交测试纪律"}, 0)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(got, "提交前先跑 make test") {
		t.Errorf("检索结果缺少条目正文: %q", got)
	}
	reopened := Open(dir)
	bumped := reopened.findByID("testing")
	if bumped == nil || bumped.Weight != 4.5 {
		t.Errorf("命中应强化 weight 至 4.5: %+v", bumped)
	}
	if bumped.LastAccessed.Equal(time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)) {
		t.Error("命中应刷新 last_accessed")
	}
	// 空查询报错
	if _, err := tool.Execute(map[string]any{"query": ""}, 0); err == nil {
		t.Error("空 query 应报错")
	}
}

// NewScorer：未配置 Embeddings 时回退 BigramScorer。
func TestNewScorerFallback(t *testing.T) {
	rt := config.Load(t.TempDir())
	if _, ok := NewScorer(rt).(BigramScorer); !ok {
		t.Error("Embeddings.BaseURL 为空应回退 BigramScorer")
	}
}
