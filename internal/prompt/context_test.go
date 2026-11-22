package prompt

import (
	"os"
	"strings"
	"testing"
	"time"

	"go-agent/internal/config"
	"go-agent/internal/memory"
)

// Rules：.nanoagents/rules/*.md 全文注入（"# 文件名\n内容"，多文件 "\n\n" 连接）；目录缺失返回空串。
func TestLoadRules(t *testing.T) {
	dir := t.TempDir() + "/rules"
	os.MkdirAll(dir, 0o755)
	os.WriteFile(dir+"/coding.md", []byte("use uv"), 0o644)
	os.WriteFile(dir+"/style.md", []byte("be terse"), 0o644)
	got := loadRules(dir)
	want := "# coding\nuse uv\n\n# style\nbe terse"
	if got != want {
		t.Errorf("loadRules = %q, want %q", got, want)
	}
	if got := loadRules(dir + ".missing"); got != "" {
		t.Errorf("loadRules(missing) = %q, want empty", got)
	}
}

// Skills：.nanoagents/skills/*.json 提取 name+description；坏文件跳过（不拖垮其余），目录缺失返回空。
func TestLoadSkills(t *testing.T) {
	dir := t.TempDir() + "/skills"
	os.MkdirAll(dir, 0o755)
	os.WriteFile(dir+"/deploy.json", []byte(`{"name":"deploy","description":"Deploy the app"}`), 0o644)
	os.WriteFile(dir+"/broken.json", []byte(`{not json`), 0o644)
	os.WriteFile(dir+"/test.json", []byte(`{"name":"test","description":"Run tests"}`), 0o644)
	got := loadSkills(dir)
	if len(got) != 2 {
		t.Fatalf("loadSkills = %v (len %d), want 2 (broken.json 应被跳过)", got, len(got))
	}
	if got[0].Name != "deploy" || got[0].Description != "Deploy the app" {
		t.Errorf("skills[0] = %+v", got[0])
	}
	if got[1].Name != "test" {
		t.Errorf("skills[1] = %+v", got[1])
	}
	if s := loadSkills(dir + ".missing"); len(s) != 0 {
		t.Errorf("loadSkills(missing) = %v, want empty", s)
	}
}

// TestBuildSystemPrompt 端到端验证 .nanoagents/ 布局下各段的注入内容与顺序
// （memory 段来自 internal/memory 的分层注入：纪律 + 索引 + 常驻条目）。
func TestBuildSystemPrompt(t *testing.T) {
	rt := config.Load(t.TempDir())
	os.MkdirAll(rt.Paths.RulesDir, 0o755)
	os.MkdirAll(rt.Paths.SkillsDir, 0o755)
	os.WriteFile(rt.Paths.NanoAgentsMD, []byte("project rules here"), 0o644)
	os.WriteFile(rt.Paths.RulesDir+"/coding.md", []byte("use go"), 0o644)
	os.WriteFile(rt.Paths.SkillsDir+"/deploy.json", []byte(`{"name":"deploy","description":"Deploy the app"}`), 0o644)
	// 预置一条高权重 memory 条目进常驻区
	ms := memory.Open(rt.Paths.MemoryDir)
	ms.Entries = []memory.Entry{{
		ID: "make-test-first", Description: "提交前先跑测试", Category: memory.CatLesson,
		Content: "提交前先跑 make test", Weight: 4,
		CreatedAt:    time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC),
		LastAccessed: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC),
	}}
	ms.Save()

	got := BuildSystemPrompt(rt)
	for _, want := range []string{
		"# Project Instructions (nanoagents.md)",
		"project rules here",
		"# Rules\n# coding\nuse go",
		"# Skills\n- deploy: Deploy the app",
		"# Memory",
		"ONE fact per file",
		"- [make-test-first](make-test-first.md) — 提交前先跑测试",
		"### make-test-first (lesson, weight 4.0)",
		"提交前先跑 make test",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("system prompt 缺少 %q，实际:\n%s", want, got)
		}
	}
	// 段顺序：Instructions → Rules → Skills → Memory
	order := []string{"# Project Instructions", "# Rules", "# Skills", "# Memory"}
	last := -1
	for _, marker := range order {
		i := strings.Index(got, marker)
		if i < 0 || i < last {
			t.Fatalf("段 %s 顺序异常（index=%d, last=%d）", marker, i, last)
		}
		last = i
	}
}
