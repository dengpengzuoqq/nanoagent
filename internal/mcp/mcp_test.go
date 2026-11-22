package mcp

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"
)

// TestHelperProcess 是测试专用子进程入口：按 env 开关扮演 fake MCP server。
// 支持三种模式：=1 正常响应；=sleep 收到请求后先睡 10s（用于超时测试）。
func TestHelperProcess(t *testing.T) {
	if os.Getenv("NANOAGENT_MCP_FAKE_SERVER") == "" {
		return
	}
	mode := os.Getenv("NANOAGENT_MCP_FAKE_SERVER")
	scanner := bufio.NewScanner(os.Stdin)
	w := bufio.NewWriter(os.Stdout)
	respond := func(id float64, result any) {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		w.Write(b)
		w.WriteByte('\n')
		w.Flush()
	}
	for scanner.Scan() {
		var msg struct {
			ID     float64 `json:"id"`
			Method string  `json:"method"`
			Params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			respond(msg.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake", "version": "0.0.1"},
			})
		case "notifications/initialized":
			// 通知无需应答
		case "tools/list":
			respond(msg.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "Echo text back",
					"inputSchema": map[string]any{
						"type":       "object",
						"properties": map[string]any{"text": map[string]any{"type": "string"}},
						"required":   []string{"text"},
					},
				}},
			})
		case "tools/call":
			if mode == "sleep" {
				time.Sleep(10 * time.Second)
			}
			var args struct {
				Text string `json:"text"`
			}
			json.Unmarshal(msg.Params.Arguments, &args)
			respond(msg.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "ECHO: " + args.Text}},
			})
		default:
			b, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": msg.ID,
				"error": map[string]any{"code": -32601, "message": "method not found: " + msg.Method},
			})
			w.Write(b)
			w.WriteByte('\n')
			w.Flush()
		}
	}
	os.Exit(0)
}

// loadMCPConfig：标准 mcpServers 配置格式；文件缺失或坏 JSON 返回空。
func TestLoadMCPConfig(t *testing.T) {
	path := t.TempDir() + "/mcp.json"
	os.WriteFile(path, []byte(`{"mcpServers":{"fake":{"command":"bin/fake","args":["-p"],"env":{"A":"1"},"disabled":false},"off":{"command":"x","disabled":true}}}`), 0o644)
	servers := LoadConfig(path)
	if len(servers) != 2 {
		t.Fatalf("loadMCPConfig = %v, want 2 servers", servers)
	}
	if servers["fake"].Command != "bin/fake" || len(servers["fake"].Args) != 1 || servers["fake"].Env["A"] != "1" {
		t.Errorf("fake server 配置错误: %+v", servers["fake"])
	}
	if !servers["off"].Disabled {
		t.Error("disabled 应为 true")
	}
	if s := LoadConfig(path + ".missing"); len(s) != 0 {
		t.Errorf("LoadConfig(missing) = %v, want empty", s)
	}
}

// mcpClient：启动子进程、initialize 握手、tools/list 发现（注册名 server__tool）、tools/call 调用与错误传播。
func TestMCPClient(t *testing.T) {
	cfg := ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		Env:     map[string]string{"NANOAGENT_MCP_FAKE_SERVER": "1"},
	}
	c, tools, err := Start("fake", cfg, 10*time.Second)
	if err != nil {
		t.Fatalf("mcpStart: %v", err)
	}
	defer Shutdown(c)
	if len(tools) != 1 || tools[0].Registered != "fake__echo" || tools[0].Name != "echo" {
		t.Fatalf("发现的工具 = %+v, want fake__echo", tools)
	}
	got, err := c.CallTool("echo", map[string]any{"text": "hi"}, 10*time.Second)
	if err != nil || got != "ECHO: hi" {
		t.Errorf("callTool = %q, err=%v, want ECHO: hi", got, err)
	}
}

func TestMCPClientTimeout(t *testing.T) {
	cfg := ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		Env:     map[string]string{"NANOAGENT_MCP_FAKE_SERVER": "sleep"},
	}
	c, _, err := Start("slow", cfg, 10*time.Second)
	if err != nil {
		t.Fatalf("mcpStart: %v", err)
	}
	defer Shutdown(c)
	if _, err := c.CallTool("echo", map[string]any{"text": "hi"}, 300*time.Millisecond); err == nil {
		t.Error("超时应返回错误")
	}
}

// TestMCPClientConcurrent：并发 CallTool 必须都成功——串行契约由 Client 内部保证，
// 调用方无需知道隐藏顺序规则。（无互斥时 nextID 竞态 + 响应互吞，-race 下必失败。）
func TestMCPClientConcurrent(t *testing.T) {
	cfg := ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		Env:     map[string]string{"NANOAGENT_MCP_FAKE_SERVER": "1"},
	}
	c, _, err := Start("fake", cfg, 10*time.Second)
	if err != nil {
		t.Fatalf("mcpStart: %v", err)
	}
	defer Shutdown(c)
	var wg sync.WaitGroup
	results := make([]string, 2)
	errs := make([]error, 2)
	for i, text := range []string{"one", "two"} {
		wg.Add(1)
		go func(i int, text string) {
			defer wg.Done()
			results[i], errs[i] = c.CallTool("echo", map[string]any{"text": text}, 5*time.Second)
		}(i, text)
	}
	wg.Wait()
	for i, text := range []string{"one", "two"} {
		if errs[i] != nil {
			t.Errorf("并发调用 %d 错误: %v", i, errs[i])
			continue
		}
		if want := "ECHO: " + text; results[i] != want {
			t.Errorf("并发调用 %d = %q, want %q", i, results[i], want)
		}
	}
}
