package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-agent/internal/config"
)

// IndexFile 是 MEMORY.md 索引文件名：纯索引，禁止写记忆正文。
const IndexFile = "MEMORY.md"

// Store 是 memory/ 目录的内存视图：条目集合 + 旧格式迁移候选。
type Store struct {
	Dir     string
	Entries []Entry
	Legacy  []string // 旧格式时间线条目原文（.nanoagents/memory.md），惰性迁移候选
}

// Open 加载 dir 下全部条目文件（跳过 MEMORY.md；损坏文件跳过并告警），
// 并检测旧格式时间线文件（dir 上级的 memory.md）作为迁移候选。
func Open(dir string) *Store {
	s := &Store{Dir: dir}
	files, err := os.ReadDir(dir)
	if err != nil {
		return s // 目录不存在 = 空记忆
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") || f.Name() == IndexFile {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		e, err := parseEntry(f.Name(), data)
		if err != nil {
			config.Logger.Warn("memory 条目解析失败，跳过", "file", f.Name(), "err", err)
			continue
		}
		s.Entries = append(s.Entries, e)
	}
	sortEntries(s.Entries)
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(dir), legacyMemoryFile)); err == nil {
		s.Legacy = parseLegacyTimeline(string(data))
	}
	return s
}

// legacyMemoryFile 是旧格式时间线文件名（相对 .nanoagents/）。
const legacyMemoryFile = "memory.md"

// parseLegacyTimeline 拆解旧格式时间线（"## 时间戳\n**Task:** ...\n**Result:** ..."）。
func parseLegacyTimeline(data string) []string {
	var blocks []string
	for _, block := range strings.Split(strings.TrimSpace(data), "\n## ") {
		if block = strings.TrimSpace(block); block != "" {
			if !strings.HasPrefix(block, "## ") {
				block = "## " + block
			}
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
}

// Size 返回全部条目序列化后的总字节数（目录容量检查）。
func (s *Store) Size() int {
	total := 0
	for _, e := range s.Entries {
		total += len(e.serialize())
	}
	return total
}

// OverLimit 报告目录是否超出容量上限。
func (s *Store) OverLimit() bool { return s.Size() > DirLimitBytes }

// Decay 执行压缩前的程序化衰减：按 last_accessed 距今天数线性衰减
// （DecayPerDay/天），低于 DecayFloor 的未 pinned 条目淘汰。pinned 条目只衰减不淘汰。
func (s *Store) Decay(now time.Time) {
	kept := s.Entries[:0]
	for _, e := range s.Entries {
		days := now.Sub(e.LastAccessed).Hours() / 24
		if days < 0 {
			days = 0
		}
		e.Weight -= DecayPerDay * days
		if e.Pinned || e.Weight >= DecayFloor {
			kept = append(kept, e)
		} else {
			config.Logger.Debug("memory 条目衰减淘汰", "id", e.ID, "weight", e.Weight)
		}
	}
	s.Entries = kept
}

// ApplyActions 应用提取调用的生命周期决策。先整批校验再应用（原子性）：
// 任一动作非法则整批放弃、memory 保持原样——与提取失败静默降级的语义一致。
func (s *Store) ApplyActions(actions []Action, now time.Time) error {
	for _, a := range actions {
		switch a.Op {
		case "ADD":
			if a.ID == "" || a.Content == "" || !validCategory(a.Category) {
				return fmt.Errorf("ADD 动作字段不合法: %+v", a)
			}
			if s.findByID(a.ID) != nil {
				return fmt.Errorf("ADD 目标已存在（应使用 UPDATE）: %s", a.ID)
			}
		case "UPDATE":
			if s.findByID(a.ID) == nil {
				return fmt.Errorf("UPDATE 目标不存在: %s", a.ID)
			}
		case "DELETE":
			if s.findByID(a.ID) == nil {
				return fmt.Errorf("DELETE 目标不存在: %s", a.ID)
			}
		default:
			return fmt.Errorf("未知动作: %q", a.Op)
		}
	}
	for _, a := range actions {
		switch a.Op {
		case "ADD":
			s.Entries = append(s.Entries, newEntryFromAction(a, now))
		case "UPDATE":
			applyUpdate(s.findByID(a.ID), a, now)
		case "DELETE":
			for i := range s.Entries {
				if s.Entries[i].ID == a.ID {
					s.Entries = append(s.Entries[:i], s.Entries[i+1:]...)
					break
				}
			}
		}
	}
	sortEntries(s.Entries)
	return nil
}

func newEntryFromAction(a Action, now time.Time) Entry {
	weight := a.Weight
	if weight <= 0 {
		weight = DecayFloor // 提取未打分时给最低初始权重
	}
	if weight > WeightMax {
		weight = WeightMax
	}
	return Entry{
		ID: a.ID, Description: firstLine(a.Content), Category: a.Category,
		Content: a.Content, Tags: a.Tags, Weight: weight, Pinned: a.Pinned,
		CreatedAt: now, LastAccessed: now, Source: a.Source,
	}
}

func applyUpdate(e *Entry, a Action, now time.Time) {
	if a.Content != "" {
		e.Content = a.Content
		e.Description = firstLine(a.Content)
	}
	if a.Category != "" {
		e.Category = a.Category
	}
	if a.Tags != nil {
		e.Tags = a.Tags
	}
	if a.Weight > 0 {
		e.Weight = a.Weight
	}
	e.Pinned = e.Pinned || a.Pinned
	e.Source = a.Source
	e.LastAccessed = now
}

func (s *Store) findByID(id string) *Entry {
	for i := range s.Entries {
		if s.Entries[i].ID == id {
			return &s.Entries[i]
		}
	}
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	r := []rune(line)
	if len(r) > IndexLineLimit {
		line = string(r[:IndexLineLimit])
	}
	return line
}

// Save 持久化：写全部条目文件、删除已不存在条目的陈旧文件、重建 MEMORY.md 索引。
func (s *Store) Save() error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, e := range s.Entries {
		if err := os.WriteFile(filepath.Join(s.Dir, e.ID+".md"), e.serialize(), 0o644); err != nil {
			return err
		}
		keep[e.ID+".md"] = true
	}
	files, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") && f.Name() != IndexFile && !keep[f.Name()] {
			if err := os.Remove(filepath.Join(s.Dir, f.Name())); err != nil {
				return err
			}
		}
	}
	return s.writeIndex()
}

func (s *Store) writeIndex() error {
	var b strings.Builder
	for _, e := range s.Entries {
		line := fmt.Sprintf("- [%s](%s.md) — %s", e.ID, e.ID, e.Description)
		if r := []rune(line); len(r) > IndexLineLimit {
			line = string(r[:IndexLineLimit])
		}
		b.WriteString(line + "\n")
	}
	return os.WriteFile(filepath.Join(s.Dir, IndexFile), []byte(b.String()), 0o644)
}

// IndexSection 返回注入用索引文本（200 行 / 25KB 限额内）。
func (s *Store) IndexSection() string {
	data, err := os.ReadFile(filepath.Join(s.Dir, IndexFile))
	if err != nil {
		return ""
	}
	text := string(data)
	if len(text) > IndexMaxBytes {
		text = text[:IndexMaxBytes]
	}
	lines := strings.Split(text, "\n")
	if len(lines) > IndexMaxLines {
		lines = lines[:IndexMaxLines]
	}
	return strings.Join(lines, "\n")
}
