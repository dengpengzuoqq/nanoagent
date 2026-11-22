package memory

import (
	"sort"
	"strings"
	"unicode"
)

// Scorer 是 memory_search 的相似度打分接口。实现：BigramScorer（零依赖默认）
// 与 EmbeddingScorer（端点支持 embeddings 时）。二者可互换——接口不变换打分函数。
type Scorer interface {
	Score(query string, entries []Entry) []Scored
}

// Scored 是一条检索命中：条目及其与查询的相关度得分。
type Scored struct {
	Entry Entry
	Score float64
}

// BigramScorer 用字符 bigram 重合度（containment）× (1 + weight/10) 打分。
// 零依赖、对中文按字成对切分，短事实条目场景够用。
type BigramScorer struct{}

func (BigramScorer) Score(query string, entries []Entry) []Scored {
	q := bigrams(query)
	var out []Scored
	for _, e := range entries {
		text := strings.ToLower(e.Content + " " + e.Description + " " + strings.Join(e.Tags, " "))
		overlap := containment(q, bigrams(text))
		if overlap <= 0 {
			continue
		}
		out = append(out, Scored{Entry: e, Score: overlap * (1 + e.Weight/10)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// bigrams 提取小写化后的相邻字符对（仅保留字母/数字/汉字，跳过空白与标点）。
func bigrams(s string) map[string]struct{} {
	runes := []rune(strings.ToLower(s))
	var kept []rune
	for _, r := range runes {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			kept = append(kept, r)
		}
	}
	set := make(map[string]struct{}, len(kept))
	for i := 0; i+1 < len(kept); i++ {
		set[string(kept[i:i+2])] = struct{}{}
	}
	return set
}

// containment 计算查询 bigram 集合在文本 bigram 集合中的覆盖率（0-1）。
func containment(query, text map[string]struct{}) float64 {
	if len(query) == 0 {
		return 0
	}
	hit := 0
	for g := range query {
		if _, ok := text[g]; ok {
			hit++
		}
	}
	return float64(hit) / float64(len(query))
}
