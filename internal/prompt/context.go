// Package prompt 负责上下文注入与跨任务记忆：system prompt 组装、rules/skills 加载、memory 读写。
package prompt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go-agent/internal/config"
	"go-agent/internal/memory"
)

// BuildSystemPrompt 组装 system prompt，顺序固定：
// 基础指令 + nanoagents.md + rules 全文 + skills 列表 + memory 末段。
func BuildSystemPrompt(rt config.Runtime) string {
	parts := []string{"You are a helpful assistant that can interact with the system. Be concise."}
	if md := config.LoadNanoAgentsMD(rt.Paths.NanoAgentsMD); md != "" {
		parts = append(parts, "# Project Instructions (nanoagents.md)\n"+md)
	}
	if rules := loadRules(rt.Paths.RulesDir); rules != "" {
		config.Logger.Debug("已加载规则", "chars", len(rules))
		parts = append(parts, "# Rules\n"+rules)
	}
	if skills := loadSkills(rt.Paths.SkillsDir); len(skills) > 0 {
		fmt.Printf("[Skills] Loaded %d skills\n", len(skills))
		lines := make([]string, len(skills))
		for i, s := range skills {
			lines[i] = fmt.Sprintf("- %s: %s", s.Name, s.Description)
		}
		parts = append(parts, "# Skills\n"+strings.Join(lines, "\n"))
	}
	// Memory：常驻区（索引 + 高权重条目正文）与写入纪律（长尾靠 memory_search 检索）
	parts = append(parts, memory.SystemSection(memory.Open(rt.Paths.MemoryDir)))
	return strings.Join(parts, "\n\n")
}

// skillInfo 是 .nanoagents/skills/*.json 中提取的字段，仅用于注入 system prompt（告知模型有什么技能，不可调用）。
type skillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// loadRules 读取 .nanoagents/rules/*.md，拼成 "# 文件名\n内容"，多文件用空行连接；目录缺失返回空串。
func loadRules(dir string) string {
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(files) == 0 {
		return ""
	}
	sort.Strings(files)
	var parts []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			config.Logger.Warn("读取规则文件失败，跳过", "path", f, "err", err)
			continue
		}
		parts = append(parts, fmt.Sprintf("# %s\n%s", strings.TrimSuffix(filepath.Base(f), ".md"), string(data)))
	}
	return strings.Join(parts, "\n\n")
}

// loadSkills 读取 .nanoagents/skills/*.json 的 name+description；坏文件跳过（不拖垮其余），目录缺失返回空。
func loadSkills(dir string) []skillInfo {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		return nil
	}
	sort.Strings(files)
	var skills []skillInfo
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			config.Logger.Warn("读取技能文件失败，跳过", "path", f, "err", err)
			continue
		}
		var s skillInfo
		if err := json.Unmarshal(data, &s); err != nil {
			config.Logger.Warn("技能文件 JSON 解析失败，跳过", "path", f, "err", err)
			continue
		}
		skills = append(skills, s)
	}
	return skills
}
