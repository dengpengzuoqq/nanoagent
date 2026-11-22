# Memory 重设计 Spec

- **状态**：设计定稿（2026-11-22，两轮 grilling + 实现/产品/学术三轮调研收敛）
- **目标读者**：主会话——按此 spec 实现，无需回溯对话
- **前置事实**：当前 OPENAI_BASE_URL（qoder）不支持 embeddings（已实测 404：only chat completions are served）

## 1. 目标

重做 nanoAgent 的跨任务记忆：从"机械追加 task+result 原文 + 末 50 行注入"升级为带生命周期、元数据与相似度检索的结构化记忆。三个硬需求：条目可更新/删除/过期；条目带标签与权重；条目支持相似度检索。

## 2. 非目标

- 不引入任何新第三方依赖（embedding 走本地 llama-server 的 OpenAI 兼容端点，向量余弦在 Go 内手写）
- 不做 sqlite FTS（规模不配 + 依赖成本 + 动态内容进 instructions 会破坏前缀 KV 缓存；检索结果只进对话历史）
- 不做用户级全局记忆层、后台 sleep-time 整理、RL 训练记忆策略（留升级位）

## 3. 条目模型

```go
type Entry struct {
    ID            string
    Category      string    // "lesson" | "fact" | "preference" | "constraint"（教训|事实|偏好|约束）
    Content       string    // 第三人称原子事实，非原文（mem0 经验）
    Tags          []string
    Weight        float64   // 初始 1-5（提取时 LLM 打分）
    Pinned        bool      // 约束类可标记，永不淘汰
    CreatedAt     time.Time
    LastAccessed  time.Time
    Source        string    // 产生该条目的任务摘要
}
```

## 4. 存储格式

- `.nanoagents/memory.md`：frontmatter + 条目列表 markdown，**事实源**，人可直接读改
- `.nanoagents/memory.json`：程序索引缓存（weight/tags/检索）
- 一致性：**单写者**——每次写先改 md 再整体重建 json

## 5. 写入：任务末一次 LLM 提取（mem0 / Memory-R1 模式）

- 输入：现有**全量条目**（32KB 内装得下）+ 本次 task、最终结果、关键工具输出
- 输出：JSON 决策，每条候选事实 **ADD / UPDATE / DELETE** 三选一（禁止纯追加——mem0 v3 单遍 ADD-only 导致堆积，issue #4956）
- 旧格式时间线（现状 agent_memory.md 式条目）：首次运行由同一次提取调用整体改写迁移（惰性）
- 提取失败静默降级：memory 保持原样，不影响本次任务结果
- 写入时机为"任务末"是合理简化；接口上留出中途写入余地（Generative Agents 重要度阈值反思）

## 6. 权重与过期（MemoryBank + Generative Agents 混合）

- `weight`：提取时 LLM 打初始 1–5 → 检索命中 +0.5（封顶 10）→ 压缩时按 `LastAccessed` 天数线性衰减
- **软过期**：衰减后低于阈值才淘汰；`pinned` 条目永不淘汰
- 触发：文件超 **32KB** 或条目数超上限 → 压缩与提取合并为同一次调用

## 7. 读取：分层注入 + 检索工具（MemGPT core/recall 简化）

- **常驻区**：高权重条目按 category 分区限量注入 system prompt 末段（字节稳定，保 KV 缓存）
- **长尾区**：新增 `memory_search` 工具（工具表 8→9，`ReadOnly=true`）按需检索
- **相似度**：字符 bigram 重合度 × weight 加权排序 top-k；命中条目 weight +0.5
- 检索结果进对话历史（function_call_output，append-only），**不进 instructions**

## 8. 嵌入路径（已定稿）

- 引擎：**llama-server**（单机桌面最轻；vLLM/SGLang 经调研判定过重：torch 全家桶、吞吐优势吃不到、SGLang CPU 仅 Xeon AMX）
- 模型：`Qwen/Qwen3-Embedding-0.6B-GGUF` **F16**（1142 MB，1024 维；避开重度量化质量风险；Q8_0 610 MB 为可选轻量替代）
- 启动：`llama-server -hf Qwen/Qwen3-Embedding-0.6B-GGUF:F16 --embeddings --port 8081`
- 接入：`Runtime.Embeddings{BaseURL: "http://127.0.0.1:8081/v1", Model: "qwen3-embedding", APIKey}`；**BaseURL 为空回退 bigram 打分**
- **上线冒烟校验**：相似中文句对得分 > 无关句对，不通过保持 bigram
- 升级闭环：`memory_search` 的 Tool interface 不变，打分函数 bigram ↔ 余弦可互换

## 9. 实现切片（每片一 commit，随片 TDD）

1. **条目模型**：Entry + md/json 解析序列化 + 单测
2. **提取**：LLM ADD/UPDATE/DELETE 调用 + 惰性迁移 + 32KB 超限压缩 + fake server 测试
3. **检索与注入**：`memory_search` 工具（bigram + 命中强化）+ `Runtime.Embeddings` 子配置（余弦打分 + 冒烟校验 + 空回退）+ 分层注入 + 单测
4. **接线与定稿**：main 调用提取、注入模板/提取 prompt 定稿、GLOSSARY 更新（新增术语：Memory Entry / Extraction / memory_search）

## 10. 验收要点

- memory 相关测试经 `Runtime` 注入 TempDir，不新增 `t.Chdir`
- 提取/压缩调用失败时任务结果不受影响（静默）
- 同一 memory 状态下多轮请求注入字节不变（前缀缓存不破）
- `go test -race ./...` 全绿
- 注入模板与提取 prompt 措辞随切片 4 出稿过目

## 11. 学术依据

- 操作分类（写/管/读）：Zhang et al., arXiv:2404.13501
- 内容四分法：CoALA, arXiv:2309.02427
- 条目生命周期 ADD/UPDATE/DELETE：Memory-R1, arXiv:2508.19828
- 衰减+强化：MemoryBank, arXiv:2305.10250
- 分层常驻/外库：MemGPT, arXiv:2310.08560
- 原子事实+元数据：A-Mem, arXiv:2502.12110
- 实现层调研：Claude Code auto-memory（索引+按需读）、ChatGPT memory（模型主动 save）、mem0（提取管线与 ADD/UPDATE/DELETE）、LangMem（hot path vs background）、Cursor Memories（审核环节）——机制报告见调研记录
