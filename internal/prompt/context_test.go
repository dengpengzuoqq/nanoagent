package prompt

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"go-agent/internal/config"
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

// Memory：载入末 50 行 + 追加时间戳条目。
func TestLoadMemoryTail(t *testing.T) {
	path := t.TempDir() + "/memory.md"
	var content string
	for i := 1; i <= 60; i++ {
		content += fmt.Sprintf("line-%d\n", i)
	}
	os.WriteFile(path, []byte(content), 0o644)
	got := loadMemory(path)
	// 字符串按 \n 切分在末尾换行后多出空元素，占掉尾部 50 行之一，
	// 因此实际载入 line-12..line-60 + 尾部空行。
	for i := 1; i <= 11; i++ {
		if strings.Contains(got, fmt.Sprintf("line-%d\n", i)) {
			t.Errorf("loadMemory 不应包含 line-%d，却看到了", i)
		}
	}
	for i := 12; i <= 60; i++ {
		if !strings.Contains(got, fmt.Sprintf("line-%d\n", i)) {
			t.Errorf("loadMemory 缺少 line-%d", i)
		}
	}
	// 文件缺失返回空串
	if got := loadMemory(path + ".missing"); got != "" {
		t.Errorf("loadMemory(missing) = %q, want empty", got)
	}
}

func TestAppendMemory(t *testing.T) {
	// 用嵌套子目录路径，验证 AppendMemory 会自动创建父目录
	path := t.TempDir() + "/.nanoagents/memory.md"
	AppendMemory(path, "my task", "my result")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, marker := range []string{"## ", "**Task:** my task", "**Result:** my result"} {
		if !strings.Contains(s, marker) {
			t.Errorf("appendMemory 输出缺少 %q，实际: %q", marker, s)
		}
	}
	// 二次追加保留旧内容
	AppendMemory(path, "task2", "result2")
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "my task") || !strings.Contains(string(data), "task2") {
		t.Error("appendMemory 应追加而非覆盖")
	}
}

// TestBuildSystemPrompt 端到端验证 .nanoagents/ 布局下各段的注入内容与顺序。
func TestBuildSystemPrompt(t *testing.T) {
	rt := config.Load(t.TempDir())
	os.MkdirAll(rt.Paths.RulesDir, 0o755)
	os.MkdirAll(rt.Paths.SkillsDir, 0o755)
	os.WriteFile(rt.Paths.NanoAgentsMD, []byte("project rules here"), 0o644)
	os.WriteFile(rt.Paths.RulesDir+"/coding.md", []byte("use go"), 0o644)
	os.WriteFile(rt.Paths.SkillsDir+"/deploy.json", []byte(`{"name":"deploy","description":"Deploy the app"}`), 0o644)
	os.WriteFile(rt.Paths.MemoryPath, []byte("## 2026-01-01\n**Task:** old task\n"), 0o644)

	got := BuildSystemPrompt(rt)
	for _, want := range []string{
		"# Project Instructions (nanoagents.md)",
		"project rules here",
		"# Rules\n# coding\nuse go",
		"# Skills\n- deploy: Deploy the app",
		"# Previous Context\n## 2026-01-01\n**Task:** old task",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("system prompt 缺少 %q，实际:\n%s", want, got)
		}
	}
	// 段顺序：Instructions → Rules → Skills → Previous Context
	order := []string{"# Project Instructions", "# Rules", "# Skills", "# Previous Context"}
	last := -1
	for _, marker := range order {
		i := strings.Index(got, marker)
		if i < 0 || i < last {
			t.Fatalf("段 %s 顺序异常（index=%d, last=%d）", marker, i, last)
		}
		last = i
	}
}
