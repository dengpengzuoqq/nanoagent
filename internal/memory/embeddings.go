package memory

import (
	"context"
	"math"
	"sort"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"go-agent/internal/config"
)

// EmbeddingScorer 用本地嵌入服务（Runtime.Embeddings，OpenAI 兼容 /v1/embeddings）
// 计算语义相似度：余弦 × (1 + weight/10)。首次使用跑冒烟校验（相似中文句对
// 得分须大于无关句对），不通过则永久降级 bigram。
type EmbeddingScorer struct {
	client   openai.Client
	model    string
	fallback BigramScorer
	smoked   bool
	smokeOK  bool
}

// NewScorer 按配置选择打分器：Embeddings.BaseURL 为空回退 BigramScorer。
func NewScorer(rt config.Runtime) Scorer {
	if rt.Embeddings.BaseURL == "" {
		return BigramScorer{}
	}
	return &EmbeddingScorer{
		client: openai.NewClient(
			option.WithAPIKey(rt.Embeddings.APIKey),
			option.WithBaseURL(rt.Embeddings.BaseURL),
		),
		model: rt.Embeddings.Model,
	}
}

func (e *EmbeddingScorer) Score(query string, entries []Entry) []Scored {
	if !e.smoked {
		e.smokeOK = e.smoke()
		e.smoked = true
	}
	if !e.smokeOK {
		return e.fallback.Score(query, entries)
	}
	texts := make([]string, 0, len(entries)+1)
	texts = append(texts, query)
	for _, en := range entries {
		texts = append(texts, en.Content+" "+en.Description)
	}
	vecs, err := e.embed(texts)
	if err != nil || len(vecs) != len(texts) {
		return e.fallback.Score(query, entries)
	}
	var out []Scored
	for i, en := range entries {
		cos := cosine(vecs[0], vecs[i+1])
		if cos <= 0.1 { // 无关条目不进结果
			continue
		}
		out = append(out, Scored{Entry: en, Score: cos * (1 + en.Weight/10)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// smoke 冒烟校验：语义相关句对的余弦必须大于无关句对。
func (e *EmbeddingScorer) smoke() bool {
	vecs, err := e.embed([]string{"如何运行单元测试", "执行 go test 的方法", "今天天气很好"})
	if err != nil || len(vecs) != 3 {
		return false
	}
	return cosine(vecs[0], vecs[1]) > cosine(vecs[0], vecs[2])
}

func (e *EmbeddingScorer) embed(texts []string) ([][]float64, error) {
	resp, err := e.client.Embeddings.New(context.Background(), openai.EmbeddingNewParams{
		Model: openai.EmbeddingModel(e.model),
		Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts},
	})
	if err != nil {
		return nil, err
	}
	out := make([][]float64, len(resp.Data))
	for i, d := range resp.Data {
		out[i] = d.Embedding
	}
	return out, nil
}

// cosine 计算两个向量的余弦相似度（零向量返回 0）。
func cosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		if i >= len(b) {
			break
		}
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
