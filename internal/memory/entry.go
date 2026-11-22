// Package memory 实现跨任务记忆：条目模型（一个事实一个文件 + MEMORY.md 索引）、
// 任务末 LLM 提取（ADD/UPDATE/DELETE 生命周期）与 memory_search 检索工具。
// 设计依据：GLOSSARY.md 的 Memory Entry / Memory Index / memory_search / Extraction 术语。
package memory

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 合法类别（CoALA 四分法的任务经验映射）。
const (
	CatLesson     = "lesson"     // 教训：失败/成功路径与原因
	CatFact       = "fact"       // 事实：项目与环境的客观信息
	CatPreference = "preference" // 偏好：用户的做法偏好
	CatConstraint = "constraint" // 约束：必须遵守的项目规则
)

// 容量与治理常量。
const (
	DirLimitBytes  = 32 * 1024 // memory/ 目录总量上限，超限触发压缩
	IndexMaxLines  = 200       // MEMORY.md 索引注入行数上限
	IndexMaxBytes  = 25 * 1024 // MEMORY.md 索引注入字节上限
	IndexLineLimit = 150       // 索引单行字符上限
	WeightMax      = 10.0      // 权重封顶
	DecayPerDay    = 0.1       // 压缩时按天线性衰减
	DecayFloor     = 1.0       // 衰减后低于该权重的未 pinned 条目淘汰
	dateFormat     = "2006-01-02"
)

// Entry 是一条跨任务记忆：一个事实一个文件，ID 即文件名（kebab-slug）。
type Entry struct {
	ID           string    `json:"id"`          // kebab-slug，frontmatter 的 name
	Description  string    `json:"description"` // 一行摘要，检索相关性判断用
	Category     string    `json:"category"`    // lesson | fact | preference | constraint
	Content      string    `json:"content"`     // 正文；lesson/constraint 含 Why:/How to apply:
	Tags         []string  `json:"tags"`
	Weight       float64   `json:"weight"`
	Pinned       bool      `json:"pinned"`
	CreatedAt    time.Time `json:"created_at"`
	LastAccessed time.Time `json:"last_accessed"`
	Source       string    `json:"source"`
}

func validCategory(c string) bool {
	return c == CatLesson || c == CatFact || c == CatPreference || c == CatConstraint
}

// Action 是提取调用输出的生命周期决策（Memory-R1 的 ADD/UPDATE/DELETE 动作空间）。
type Action struct {
	Op       string   `json:"op"` // ADD | UPDATE | DELETE
	ID       string   `json:"id"` // 目标条目（ADD 时为新建条目的 kebab-slug）
	Category string   `json:"category"`
	Content  string   `json:"content"`
	Tags     []string `json:"tags"`
	Weight   float64  `json:"weight"`
	Pinned   bool     `json:"pinned"`
	Source   string   `json:"source"`
}

// parseEntry 解析条目文件（frontmatter + 正文）；frontmatter 缺失或损坏返回错误。
// 正文不应包含 "\n---\n"（条目分界符）。
func parseEntry(file string, data []byte) (Entry, error) {
	e := Entry{ID: strings.TrimSuffix(file, ".md")}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return e, fmt.Errorf("%s: 缺少 frontmatter", file)
	}
	parts := strings.SplitN(strings.TrimPrefix(text, "---\n"), "\n---\n", 2)
	if len(parts) != 2 {
		return e, fmt.Errorf("%s: frontmatter 未闭合", file)
	}
	for _, line := range strings.Split(parts[0], "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			return e, fmt.Errorf("%s: frontmatter 行缺少冒号: %q", file, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		var err error
		switch key {
		case "name":
			e.ID = value
		case "description":
			e.Description = value
		case "type":
			e.Category = value
		case "tags":
			e.Tags = parseTags(value)
		case "weight":
			e.Weight, err = strconv.ParseFloat(value, 64)
		case "pinned":
			e.Pinned, err = strconv.ParseBool(value)
		case "created_at":
			e.CreatedAt, err = time.Parse(dateFormat, value)
		case "last_accessed":
			e.LastAccessed, err = time.Parse(dateFormat, value)
		case "source":
			e.Source = value
		}
		if err != nil {
			return e, fmt.Errorf("%s: frontmatter 字段 %s 解析失败: %w", file, key, err)
		}
	}
	e.Content = strings.TrimSuffix(strings.TrimPrefix(parts[1], "\n"), "\n")
	return e, nil
}

func (e Entry) serialize() []byte {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", e.ID)
	fmt.Fprintf(&b, "description: %s\n", e.Description)
	fmt.Fprintf(&b, "type: %s\n", e.Category)
	fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(e.Tags, ", "))
	fmt.Fprintf(&b, "weight: %s\n", strconv.FormatFloat(e.Weight, 'f', 1, 64))
	fmt.Fprintf(&b, "pinned: %t\n", e.Pinned)
	fmt.Fprintf(&b, "created_at: %s\n", e.CreatedAt.Format(dateFormat))
	fmt.Fprintf(&b, "last_accessed: %s\n", e.LastAccessed.Format(dateFormat))
	fmt.Fprintf(&b, "source: %s\n", e.Source)
	b.WriteString("---\n")
	b.WriteString(e.Content)
	if !strings.HasSuffix(e.Content, "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// parseTags 解析 "[a, b]" 或 "a, b" 形式的标签列表。
func parseTags(s string) []string {
	s = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(s), "]"), "[")
	if s == "" {
		return nil
	}
	var tags []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
