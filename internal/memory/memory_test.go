package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sampleEntry(id string) Entry {
	return Entry{
		ID: id, Description: "desc of " + id, Category: CatFact,
		Content: "content of " + id, Tags: []string{"go", "memory"},
		Weight: 3, CreatedAt: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC),
		LastAccessed: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC), Source: "测试任务",
	}
}

// Entry ↔ frontmatter 序列化往返一致。
func TestEntryRoundTrip(t *testing.T) {
	e := sampleEntry("go-memory-design")
	e.Content = "line1\nline2\n\nWhy: tests broke\nHow to apply: run make test"
	data := e.serialize()
	got, err := parseEntry(e.ID+".md", data)
	if err != nil {
		t.Fatalf("parseEntry: %v", err)
	}
	got.CreatedAt = e.CreatedAt
	got.LastAccessed = e.LastAccessed
	if !reflect.DeepEqual(got, e) {
		t.Errorf("round trip 不一致:\n got=%+v\nwant=%+v", got, e)
	}
}

// Store：Save 写条目文件与索引，Open 读回一致；DELETE 后 Save 清理陈旧文件。
func TestStoreSaveReopen(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	if len(s.Entries) != 0 {
		t.Fatal("空目录应无条目")
	}
	s.Entries = []Entry{sampleEntry("alpha"), sampleEntry("beta")}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, id := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(dir, id+".md")); err != nil {
			t.Errorf("条目文件缺失: %s", id)
		}
	}
	idx, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		t.Fatalf("索引缺失: %v", err)
	}
	if !strings.Contains(string(idx), "- [alpha](alpha.md) — desc of alpha") {
		t.Errorf("索引行格式异常: %q", idx)
	}
	// 删除 beta → Save 清理文件
	s.Entries = s.Entries[:1]
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "beta.md")); !os.IsNotExist(err) {
		t.Error("删除条目的文件应被清理")
	}
	// 重开读回一致
	s2 := Open(dir)
	if len(s2.Entries) != 1 || s2.Entries[0].ID != "alpha" {
		t.Errorf("重开条目 = %+v", s2.Entries)
	}
}

// 索引行超长截断到 IndexLineLimit。
func TestIndexLineTruncation(t *testing.T) {
	s := Open(t.TempDir())
	s.Entries = []Entry{sampleEntry("long")}
	s.Entries[0].Description = strings.Repeat("很长的描述", 100)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	idx, _ := os.ReadFile(filepath.Join(s.Dir, IndexFile))
	for _, line := range strings.Split(strings.TrimRight(string(idx), "\n"), "\n") {
		if r := []rune(line); len(r) > IndexLineLimit {
			t.Errorf("索引行超限 %d 字符: %q", len(r), line)
		}
	}
}

// ApplyActions：ADD/UPDATE/DELETE 三种生命周期动作、整批校验的原子性与错误分支。
func TestApplyActions(t *testing.T) {
	s := Open(t.TempDir())
	s.Entries = []Entry{sampleEntry("old")}
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)

	// 原子性：批内任一动作非法 → 整批放弃
	err := s.ApplyActions([]Action{
		{Op: "ADD", ID: "fresh", Category: CatLesson, Content: "先跑 make test 再提交", Tags: []string{"testing"}, Weight: 4, Source: "新任务"},
		{Op: "DELETE", ID: "gone"},
	}, now)
	if err == nil {
		t.Fatal("DELETE 不存在的条目应报错")
	}
	if s.findByID("fresh") != nil {
		t.Error("校验失败应整批放弃，不应部分应用")
	}

	if err := s.ApplyActions([]Action{{Op: "ADD", ID: "old", Category: CatFact, Content: "dup"}}, now); err == nil {
		t.Fatal("ADD 已存在的条目应报错（应使用 UPDATE）")
	}

	err = s.ApplyActions([]Action{
		{Op: "ADD", ID: "fresh", Category: CatLesson, Content: "先跑 make test 再提交", Tags: []string{"testing"}, Weight: 4, Source: "新任务"},
		{Op: "UPDATE", ID: "old", Content: "更新后的事实"},
	}, now)
	if err != nil {
		t.Fatalf("ApplyActions: %v", err)
	}
	fresh := s.findByID("fresh")
	if fresh == nil || fresh.Category != CatLesson || fresh.Weight != 4 || fresh.LastAccessed != now {
		t.Errorf("ADD 结果异常: %+v", fresh)
	}
	old := s.findByID("old")
	if old == nil || old.Content != "更新后的事实" || old.Description != "更新后的事实" {
		t.Errorf("UPDATE 结果异常: %+v", old)
	}
	if err := s.ApplyActions([]Action{{Op: "ADD", ID: "badcat", Category: "nope", Content: "x"}}, now); err == nil {
		t.Error("非法类别应报错")
	}
}

// Decay：按天线性衰减，低于 floor 的未 pinned 条目淘汰，pinned 只衰减不淘汰。
func TestDecay(t *testing.T) {
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	s := Open(t.TempDir())
	old := sampleEntry("old") // 30 天未访问：3 - 3 = 0 < 1 → 淘汰
	old.LastAccessed = now.AddDate(0, 0, -30)
	pinned := sampleEntry("pinned") // pinned：衰减但保留
	pinned.Category = CatConstraint
	pinned.Pinned = true
	pinned.LastAccessed = now.AddDate(0, 0, -30)
	fresh := sampleEntry("fresh") // 当天访问：不衰减
	fresh.LastAccessed = now
	s.Entries = []Entry{old, pinned, fresh}
	s.Decay(now)
	if s.findByID("old") != nil {
		t.Error("衰减到 floor 以下的未 pinned 条目应被淘汰")
	}
	p := s.findByID("pinned")
	if p == nil || p.Weight != 0 {
		t.Errorf("pinned 应保留且已衰减: %+v", p)
	}
	if f := s.findByID("fresh"); f == nil || f.Weight != 3 {
		t.Errorf("新近条目不应衰减: %+v", f)
	}
}

// 旧格式时间线解析（迁移候选）。
func TestLegacyTimeline(t *testing.T) {
	legacy := "## 2026-11-20 15:04:05\n**Task:** 任务A\n**Result:** 结果A\n\n## 2026-11-21 10:00:00\n**Task:** 任务B\n**Result:** 结果B\n"
	blocks := parseLegacyTimeline(legacy)
	if len(blocks) != 2 {
		t.Fatalf("时间线块数 = %d, want 2", len(blocks))
	}
	if !strings.Contains(blocks[0], "任务A") || !strings.HasPrefix(blocks[0], "## 2026-11-20") {
		t.Errorf("块 0 异常: %q", blocks[0])
	}
	// Open 检测旧文件为迁移候选
	dir := t.TempDir()
	os.WriteFile(filepath.Join(filepath.Dir(dir), legacyMemoryFile), []byte(legacy), 0o644)
	s := Open(dir)
	if len(s.Legacy) != 2 {
		t.Errorf("Legacy 迁移候选 = %d, want 2", len(s.Legacy))
	}
}
