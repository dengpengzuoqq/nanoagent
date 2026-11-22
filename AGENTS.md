# AGENTS.md

本文件是工程 skill 的配置入口。运行时注入 fixture 是 `nanoagents.md`（gitignore），与本文件无关。

## Agent skills

### Issue tracker

Issue 以本地 markdown 存于 `.scratch/<feature>/`，不使用 GitHub Issues。See `docs/agents/issue-tracker.md`.

### Domain docs

single-context：根目录 `GLOSSARY.md` + `docs/adr/`（尚不存在时相关 skill 惰性创建）。See `docs/agents/domain.md`.
