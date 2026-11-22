# GLOSSARY

项目领域术语。工程 skill（架构审查、grilling、测试命名）使用这里的词汇，不另造同义词。

- **Runtime**：`config.Load(root)` 返回的值，携带 test.env 配置、绝对路径布局（Paths）与 Responses API client。main 用 `Load(".")` 构造并显式下发；测试用 `Load(t.TempDir())` 提供 seam 的第二个 adapter。
- **Tool**：工具 registry 的 interface 单元（Name / Description / Schema / ReadOnly / Execute）。三种 adapter：本地工具（`tools.LocalTools`）、计划模式工具（`plan.PlanTools`）、MCP 工具（`mcp.Tool` 包装）。
- **Decision**：`plan.SubmitPlan` 审批的结果值（approve / reject / fatal）。fatal 经 `plan.FatalError` 走返回值通道终止运行，替代旧的 FatalErr 控制流旁路；Kind 是 fatal 的唯一裁决。submit_plan 的前置条件（仅 plan 态、plan 非空）由 registry Execute 层校验，错误统一走回填编码。
- **Plan mode**：只读探索态。拦截规则 = 查 `Tool.ReadOnly`，不按工具名硬编码；非只读工具在 plan 态被拦截并指明 submit_plan 出路。
- **Memory Entry**：跨任务记忆最小单元——`memory/` 目录下一个事实一个文件（kebab-slug 即 ID），frontmatter 含 name/description/type(lesson|fact|preference|constraint)/tags/weight/pinned/created_at/last_accessed/source；`MEMORY.md` 为纯索引（每行 ≤150 字符），禁止写正文。
- **Extraction**：任务末一次 LLM 调用（memory.Extract），对全量条目做 ADD/UPDATE/DELETE 决策（Memory-R1 动作空间），兼任压缩与旧格式惰性迁移；先整批校验再应用（原子性），失败静默保持 memory 原样。
- **memory_search**：第 9 个工具（ReadOnly），长尾记忆检索——bigram 重合度 × weight 加权 top-k，命中强化 weight +0.5（封顶 10）并刷新 last_accessed。
- **Embedding fallback**：`Runtime.Embeddings`（本地 llama-server + Qwen3-Embedding-0.6B F16）配置后 memory_search 打分切换为嵌入余弦，上线冒烟校验不过或未配置时回退 bigram；检索结果只进对话历史，不进 instructions。
- **升级备忘**：scratchpad 层（pi-memory 的会话工作记忆）、sqlite FTS、全局记忆层、sleep-time 整理、RL 记忆策略——均暂不做，留升级位。
