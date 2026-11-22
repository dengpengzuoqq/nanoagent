// Package config 提供运行时配置：test.env 读取、nanoagents.md 加载、Responses API 客户端与日志器。
package config

import (
	"log/slog"
	"os"
	"path/filepath"
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

// Paths 是运行时文件与目录的布局定义（相对 root）。
type Paths struct {
	NanoAgentsMD  string // system prompt 注入的项目说明
	RulesDir      string // 规则全文 .nanoagents/rules/*.md
	SkillsDir     string // 技能列表 .nanoagents/skills/*.json
	MemoryDir     string // 跨任务记忆目录（MEMORY.md 索引 + 每事实一文件）
	MemoryPath    string // 旧格式时间线文件（迁移候选，删除于 memory 改造接线完成时）
	MCPConfigPath string // MCP server 配置
}

func (p Paths) join(root string) Paths {
	j := func(rel string) string { return filepath.Join(root, rel) }
	return Paths{
		NanoAgentsMD:  j(p.NanoAgentsMD),
		RulesDir:      j(p.RulesDir),
		SkillsDir:     j(p.SkillsDir),
		MemoryDir:     j(p.MemoryDir),
		MemoryPath:    j(p.MemoryPath),
		MCPConfigPath: j(p.MCPConfigPath),
	}
}

// relativePaths 是相对 root 的布局定义，Load 负责拼接成绝对路径。
var relativePaths = Paths{
	NanoAgentsMD:  "nanoagents.md",
	RulesDir:      ".nanoagents/rules",
	SkillsDir:     ".nanoagents/skills",
	MemoryDir:     ".nanoagents/memory",
	MemoryPath:    ".nanoagents/memory.md",
	MCPConfigPath: ".nanoagents/mcp.json",
}

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

// Runtime 是一次运行所需的全部环境：配置、路径布局与 API client。
// main 用 Load(".") 构造（生产行为等同旧的相对路径）；测试用 Load(t.TempDir())。
type Runtime struct {
	Config RuntimeConfig
	Paths  Paths
	Client openai.Client
}

// Load 读取 root 下的 test.env，拼接路径布局并构造 client。
// 配置文件缺失返回空配置（不退出），由调用方校验必需键。
func Load(root string) Runtime {
	cfg := newConfig(loadEnv(filepath.Join(root, "test.env")))
	return Runtime{
		Config: cfg,
		Client: newClient(cfg),
		Paths:  relativePaths.join(root),
	}
}

func newClient(cfg RuntimeConfig) openai.Client {
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	return openai.NewClient(opts...)
}

// Model 返回配置的模型名，未配置时回退到 gpt-4o-mini。
func (r Runtime) Model() string {
	if r.Config.Model != "" {
		return r.Config.Model
	}
	return "gpt-4o-mini"
}
