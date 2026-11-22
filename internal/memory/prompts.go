package memory

import (
	"fmt"
	"sort"
	"strings"
)

// ConstantBudgetBytes 是常驻注入区（高权重条目正文）的字节预算。
const ConstantBudgetBytes = 8 * 1024

// writeRules 是系统提示中的记忆写入纪律（ZCode 验证过的质量闸门）。
const writeRules = `This project has a cross-task memory directory: ONE fact per file with frontmatter (name/description/type/tags/weight/pinned), and MEMORY.md as the index — never write memory content into MEMORY.md itself.

Write a memory file when you learn durable information:
- the user's role, preferences, or habits (type: preference)
- corrections to your approach, or a non-obvious approach that worked — include Why and How to apply (type: lesson)
- who is doing what, why, or by when in the project (type: fact)
- rules that must be followed (type: constraint, consider pinned: true)

Do NOT write: anything derivable from the code, git history, bug-fix recipes, content already covered by nanoagents.md, one-off task state. Convert relative dates to absolute. Check for an existing entry to UPDATE first; update or remove memories that turn out to be wrong. Organize semantically, not chronologically. Link related entries with [[their-id]].`

// SystemSection 组装 system prompt 的 "# Memory" 段：写入纪律 + 索引（限额内）+
// 常驻条目正文（pinned > weight 降序，预算截断）。空记忆给占位语。
// 每次任务只调用一次、任务内字节不变——保证 KV 缓存前缀稳定。
func SystemSection(s *Store) string {
	var b strings.Builder
	b.WriteString("# Memory\n\n")
	b.WriteString(writeRules)
	if len(s.Entries) == 0 && len(s.Legacy) == 0 {
		b.WriteString("\n\nYour memory is currently empty. When you save new memories, they will appear here.\n")
		return b.String()
	}
	if idx := s.IndexSection(); idx != "" {
		b.WriteString("\n\n## Index\n" + idx)
	}
	if constant := s.ConstantSection(ConstantBudgetBytes); constant != "" {
		b.WriteString("\n\n## Key entries\n" + constant)
	}
	return b.String()
}

// ConstantSection 选取常驻条目正文：pinned 优先，其次 weight 降序，预算内装载
// （装不下的跳过，靠 memory_search 按需检索）。
func (s *Store) ConstantSection(budget int) string {
	cands := append([]Entry{}, s.Entries...)
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Pinned != cands[j].Pinned {
			return cands[i].Pinned
		}
		return cands[i].Weight > cands[j].Weight
	})
	var b strings.Builder
	total := 0
	for _, e := range cands {
		block := fmt.Sprintf("### %s (%s, weight %.1f)\n%s\n\n", e.ID, e.Category, e.Weight, e.Content)
		if total+len(block) > budget {
			continue
		}
		b.WriteString(block)
		total += len(block)
	}
	return strings.TrimRight(b.String(), "\n")
}
