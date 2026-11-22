package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"go-agent/internal/config"
)

// MaxEntries 是条目数上限，超限同样触发压缩（与 32KB 目录上限二选一先到）。
const MaxEntries = 100

// TraceTailBytes 是提取输入中保留的工具轨迹尾部大小。
const TraceTailBytes = 8 * 1024

// Extract 在任务结束后运行记忆生命周期：程序化衰减（超限时）→ 一次 LLM 调用
// 产出 ADD/UPDATE/DELETE 决策（含压缩去重与旧格式惰性迁移）→ 应用并持久化。
// 任何失败都返回 error 且 memory 保持原样（调用方静默降级，不影响任务结果）。
func Extract(rt config.Runtime, task, result, trace string, now time.Time) error {
	s := Open(rt.Paths.MemoryDir)
	over := s.OverLimit() || len(s.Entries) > MaxEntries
	if over {
		s.Decay(now)
	}
	resp, err := rt.Client.Responses.New(context.Background(), responses.ResponseNewParams{
		Model: shared.ResponsesModel(rt.Model()),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: responses.ResponseInputParam{
			{OfMessage: &responses.EasyInputMessageParam{
				Role:    responses.EasyInputMessageRoleUser,
				Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(buildExtractInput(s, task, result, trace, over))},
			}},
		}},
	})
	if err != nil {
		return err
	}
	actions, err := parseActions(resp.OutputText())
	if err != nil {
		return err
	}
	if err := s.ApplyActions(actions, now); err != nil {
		return err
	}
	if err := s.Save(); err != nil {
		return err
	}
	if len(s.Legacy) > 0 {
		// 迁移完成：旧格式时间线文件删除（提取失败时保留重试）
		if err := os.Remove(filepath.Join(filepath.Dir(rt.Paths.MemoryDir), legacyMemoryFile)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

const extractRules = `你是 nanoAgent 的记忆管理器。基于本次任务记录与现有记忆，输出跨任务记忆的生命周期决策。

规则：
- 只记对未来任务有复用价值的信息：教训（失败/成功路径与原因）、事实（项目与环境的客观信息）、偏好（用户做法偏好）、约束（必须遵守的规则）
- 不记：可从代码推导的架构/路径、git 历史、修 bug 配方、项目说明（nanoagents.md）已覆盖的内容、一次性临时状态
- 粒度：一个事实一条，第三人称原子事实，禁止对话原文；相关条目在 content 中用 [[id]] 互链
- 更新优先于新增：语义重复的既有条目用 UPDATE（引用其 id），过时或错误条目用 DELETE
- category 只允许 lesson/fact/preference/constraint；id 用 kebab-slug；weight 按重要度打 1-5
- 输出严格 JSON：{"actions":[{"op":"ADD","id":"...","category":"lesson","content":"...","tags":["..."],"weight":3,"pinned":false,"source":"..."}]}；无动作时输出 {"actions":[]}
- 只输出 JSON，不要任何解释`

// buildExtractInput 组装提取调用的输入（规则 + 现有条目 + 迁移候选 + 任务记录）。
func buildExtractInput(s *Store, task, result, trace string, compacting bool) string {
	var b strings.Builder
	b.WriteString(extractRules)
	if compacting {
		b.WriteString("\n\n注意：记忆已达容量上限，本轮回合优先合并重复、淘汰低价值条目（多用 UPDATE/DELETE）。")
	}
	entries, _ := json.Marshal(s.Entries)
	b.WriteString("\n\n## 现有记忆（JSON）\n" + string(entries))
	if len(s.Legacy) > 0 {
		b.WriteString("\n\n## 旧格式历史（迁移候选：有价值则改写为 ADD，无价值可忽略，系统将删除原文件）")
		for _, block := range s.Legacy {
			b.WriteString("\n---\n" + block)
		}
	}
	b.WriteString("\n\n## 本次任务\nTask: " + task + "\nResult: " + result)
	if trace != "" {
		b.WriteString("\n\n## 工具轨迹（尾部）\n" + tail(trace, TraceTailBytes))
	}
	return b.String()
}

// parseActions 从模型输出中提取 ADD/UPDATE/DELETE 决策 JSON（容忍代码围栏与前后缀）。
func parseActions(text string) ([]Action, error) {
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, errors.New("提取输出中未找到 JSON")
	}
	var out struct {
		Actions []Action `json:"actions"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &out); err != nil {
		return nil, fmt.Errorf("提取输出 JSON 解析失败: %w", err)
	}
	return out.Actions, nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
