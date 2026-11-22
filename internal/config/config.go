// Package config 提供运行时配置：test.env 读取、nanoagents.md 加载、Responses API 客户端与日志器。
package config

import (
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// Logger 是全项目共用的结构化日志器（stderr）。
var Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

// loadEnv 读取 env 文件，返回配置 map（KEY=VALUE 格式，支持 # 注释和引号包裹的值）。
// 文件不存在时返回空 map（不退出），由调用方校验必需的配置键。
func loadEnv(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		Logger.Error("读取配置文件失败", "path", path, "err", err)
		return map[string]string{}
	}
	cfg := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		cfg[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), "'\"")
	}
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	Logger.Debug("已加载配置文件", "path", path, "keys", keys)
	return cfg
}

// LoadNanoAgentsMD 读取项目 nanoagents.md，作为 system prompt 的一部分；不存在时返回空串。
func LoadNanoAgentsMD(path string) string {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		Logger.Debug("nanoagents.md 不存在，跳过", "path", path)
		return ""
	}
	if err != nil {
		Logger.Error("读取 nanoagents.md 失败", "path", path, "err", err)
		return ""
	}
	content := strings.TrimSpace(string(data))
	Logger.Debug("已加载 nanoagents.md", "path", path, "chars", len(content))
	return content
}

// 运行时文件与目录布局，全部相对路径集中在此；运行必须在项目根目录。
const (
	NanoAgentsMD  = "./nanoagents.md"         // system prompt 注入的项目说明
	RulesDir      = "./.nanoagents/rules"     // 规则全文 .nanoagents/rules/*.md
	SkillsDir     = "./.nanoagents/skills"    // 技能列表 .nanoagents/skills/*.json
	MemoryPath    = "./.nanoagents/memory.md" // 跨任务记忆
	MCPConfigPath = "./.nanoagents/mcp.json"  // MCP server 配置
	TestEnvPath   = "./test.env"              // OpenAI 配置
)

// RuntimeConfig 是 test.env 的解析结果（对应键：OPENAI_API_KEY / OPENAI_BASE_URL / OPENAI_MODEL）。
type RuntimeConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// newConfig 从 loadEnv 的原始键值对提取已知键（未知键忽略）。
func newConfig(env map[string]string) RuntimeConfig {
	return RuntimeConfig{
		APIKey:  env["OPENAI_API_KEY"],
		BaseURL: env["OPENAI_BASE_URL"],
		Model:   env["OPENAI_MODEL"],
	}
}

// Config 是全局运行配置；Client 是 OpenAI 兼容 Responses API 客户端（BaseURL 留空时用 SDK 默认地址）。
var (
	Config = newConfig(loadEnv(TestEnvPath))
	Client = newClient(Config)
)

func newClient(cfg RuntimeConfig) openai.Client {
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	return openai.NewClient(opts...)
}

// ModelOrDefault 返回配置的模型名，未配置时回退到 gpt-4o-mini。
func ModelOrDefault() string {
	if Config.Model != "" {
		return Config.Model
	}
	return "gpt-4o-mini"
}
