# GLOSSARY

项目领域术语。工程 skill（架构审查、grilling、测试命名）使用这里的词汇，不另造同义词。

- **Runtime**：`config.Load(root)` 返回的值，携带 test.env 配置、绝对路径布局（Paths）与 Responses API client。main 用 `Load(".")` 构造并显式下发；测试用 `Load(t.TempDir())` 提供 seam 的第二个 adapter。
- **Tool**：工具 registry 的 interface 单元（Name / Description / Schema / ReadOnly / Execute）。三种 adapter：本地工具（`tools.LocalTools`）、计划模式工具（`plan.PlanTools`）、MCP 工具（`mcp.Tool` 包装）。
- **Decision**：`plan.SubmitPlan` 审批的结果值（approve / reject / fatal）。fatal 经 `plan.FatalError` 走返回值通道终止运行，替代旧的 FatalErr 控制流旁路；Kind 是 fatal 的唯一裁决。submit_plan 的前置条件（仅 plan 态、plan 非空）由 registry Execute 层校验，错误统一走回填编码。
- **Plan mode**：只读探索态。拦截规则 = 查 `Tool.ReadOnly`，不按工具名硬编码；非只读工具在 plan 态被拦截并指明 submit_plan 出路。
