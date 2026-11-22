// Package testutil 提供跨包测试辅助：假 Responses API server 与请求体收集器。
package testutil

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// BodyCollector 线程安全地收集请求体：handler goroutine 写、测试 goroutine 读，必须互斥。
type BodyCollector struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (c *BodyCollector) add(body map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies = append(c.bodies, body)
}

func (c *BodyCollector) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func (c *BodyCollector) At(i int) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bodies[i]
}

// ResponsesServer 启动假 Responses API：前 len(responses)-1 轮返回 responses[i]，
// 之后重复返回最后一个；收集每个请求体，返回 (收集器, 指向该服务的测试客户端)。
func ResponsesServer(t *testing.T, responses []string) (*BodyCollector, openai.Client) {
	t.Helper()
	c := &BodyCollector{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("请求体不是 JSON: %v", err)
		}
		c.add(body)
		w.Header().Set("Content-Type", "application/json")
		i := c.Count() - 1
		if i >= len(responses) {
			i = len(responses) - 1
		}
		io.WriteString(w, responses[i])
	}))
	t.Cleanup(srv.Close)
	return c, openai.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(srv.URL))
}
