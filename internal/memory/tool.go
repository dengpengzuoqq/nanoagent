package memory

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go-agent/internal/tools"
)

// SearchTool 构造 memory_search 工具（ReadOnly=true，plan 态可用）：
// 长尾记忆按需检索，命中条目 weight +0.5（封顶 WeightMax）并刷新
// last_accessed 后持久化。每次调用重新 Open 目录——模型运行中用
// write 工具新写的条目不会被陈旧视图覆盖。
func SearchTool(dir string, scorer Scorer) tools.Tool {
	return tools.Tool{
		Name:        "memory_search",
		Description: "Search cross-task memory (lessons/facts/preferences/constraints from past tasks in this project) by keyword or natural-language query. Use when the current task might relate to past experience",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": map[string]any{"type": "string", "description": "检索关键词或自然语言查询"}},
			"required":   []string{"query"},
		},
		ReadOnly: true,
		Execute: func(args map[string]any, timeout time.Duration) (string, error) {
			query := tools.StrArg(args, "query")
			if strings.TrimSpace(query) == "" {
				return "", errors.New("query 不能为空")
			}
			s := Open(dir)
			hits := scorer.Score(query, s.Entries)
			if len(hits) == 0 {
				return "No matching memories", nil
			}
			var b strings.Builder
			for i, h := range hits {
				if i >= 5 {
					break
				}
				fmt.Fprintf(&b, "[%d] %s (type %s, weight %.1f)\n%s\n\n", i+1, h.Entry.ID, h.Entry.Category, h.Entry.Weight, h.Entry.Content)
				s.bump(h.Entry.ID, time.Now())
			}
			if err := s.Save(); err != nil {
				return "", err
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

// bump 检索命中强化：weight +0.5 封顶 WeightMax，last_accessed 刷新。
func (s *Store) bump(id string, now time.Time) {
	if e := s.findByID(id); e != nil {
		e.Weight += 0.5
		if e.Weight > WeightMax {
			e.Weight = WeightMax
		}
		e.LastAccessed = now
	}
}
