# 本地嵌入服务安装：llama-server + Qwen3-Embedding-0.6B

为 `memory_search` 提供语义相似度打分（`Runtime.Embeddings` 子配置）。
llama-server 是**独立外部进程**，不是 Go 依赖；未启动或未配置时
`memory_search` 自动回退 bigram 词汇打分，工具照常工作（GLOSSARY「Embedding fallback」）。

## 1. 安装 llama-server

Arch / omarchy：

```bash
sudo pacman -S llama-cpp
```

其他平台：从 [llama.cpp releases](https://github.com/ggml-org/llama.cpp/releases) 下载对应平台二进制，或源码编译。

## 2. 启动模型服务

```bash
llama-server -hf ggml-org/Qwen3-Embedding-0.6B-GGUF:F16 \
  --embeddings --port 8081 --alias qwen3-embedding
```

- `-hf ...:F16`：首次运行自动从 HuggingFace 下载 **约 1.2 GB** 权重（1024 维嵌入），缓存在 `~/.cache/llama.cpp`，之后离线可用
- 下载慢可走镜像：`HF_ENDPOINT=https://hf-mirror.com llama-server ...`
- `--embeddings`：只启用 embedding 模式（Qwen3-Embedding 使用 last-token pooling，llama.cpp 按 GGUF 元数据自动处理）
- `--alias qwen3-embedding`：给模型稳定名字，供 `/v1/embeddings` 请求的 `model` 字段使用
- 0.6B 嵌入模型 CPU 推理毫秒级响应，GPU 非必需；有 GPU 后端时可加 `-ngl 999` 全量卸载

**常驻运行**（可选）——`~/.config/systemd/user/nanoagent-embeddings.service`：

```ini
[Unit]
Description=llama-server embeddings for nanoAgent (Qwen3-Embedding-0.6B)
[Service]
ExecStart=/usr/bin/llama-server -hf ggml-org/Qwen3-Embedding-0.6B-GGUF:F16 --embeddings --port 8081 --alias qwen3-embedding
Restart=on-failure
[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload && systemctl --user enable --now nanoagent-embeddings
```

## 3. 验证服务

```bash
curl -s http://127.0.0.1:8081/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{"model":"qwen3-embedding","input":"测试相似度检索"}' | head -c 200
```

返回含 `"embedding":[...]`（1024 个浮点数）即正常。

## 4. 接入 nanoAgent

项目根 `test.env` 追加三行：

```bash
OPENAI_EMBEDDINGS_BASE_URL=http://127.0.0.1:8081/v1
OPENAI_EMBEDDINGS_MODEL=qwen3-embedding
OPENAI_EMBEDDINGS_API_KEY=local
```

行为说明（代码已内置，见 `internal/memory/embeddings.go`）：

- 三个键**任一未配置或 BaseURL 为空** → `memory_search` 使用 bigram 词汇打分（零依赖路径）
- 配置后 `memory_search` 首次被调用自动跑**冒烟校验**：相似中文句对的余弦得分必须大于无关句对
- 冒烟通过 → 余弦打分；**不通过或服务不可用 → 永久降级 bigram**（进程生命周期内）
- 打分函数可互换：`Tool` interface 不变，只换 `Scorer` 实现

## 5. 常见问题

| 现象 | 处理 |
|---|---|
| 端口 8081 被占用 | 换端口启动，同步改 `OPENAI_EMBEDDINGS_BASE_URL` |
| 模型下载慢 | `HF_ENDPOINT=https://hf-mirror.com` 前缀 |
| llama-server 停了 | `memory_search` 静默回退 bigram，agent 不中断 |
| 中文检索效果 | Qwen3-Embedding 原生支持中文，无需分词配置 |
| 想换更大模型 | 换 `-hf` 标签（如 4B），`OPENAI_EMBEDDINGS_MODEL` 同步改；冒烟校验自动重新把关 |

## 6. 升级路径备忘（暂不做）

sqlite FTS、全局记忆层、sleep-time 整理、RL 记忆策略——见 GLOSSARY「升级备忘」。
当前规模（32KB / 100 条）下余弦线性扫描已足够，向量库不是必需品。
