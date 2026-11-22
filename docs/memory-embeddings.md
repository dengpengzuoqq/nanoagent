# 本地嵌入服务安装：llama-server + Qwen3-Embedding-0.6B

为 `memory_search` 提供语义相似度打分（`Runtime.Embeddings` 子配置）。
llama-server 是**独立外部进程**，不是 Go 依赖；未启动或未配置时
`memory_search` 自动回退 bigram 词汇打分，工具照常工作（GLOSSARY「Embedding fallback」）。

## 1. 安装 llama-server

本机为 AMD 集显（RENOIR，RADV Vulkan 驱动正常，共享显存已分配 **8GB**）。要用集显跑模型，需要 **Vulkan 后端构建**的 llama.cpp——Arch 仓库的 `llama-cpp` 包是 CPU-only 构建，直接安装无法 offload 到 GPU。

**GPU 路线（推荐，已在本机验证）**：llama.cpp 官方每日构建的 Vulkan 预编译包（35 MB，自包含 `libggml-vulkan` 等后端，`RUNPATH=$ORIGIN` 无需装库）：

```bash
# 从 releases 取最新 build 号（本例 b12631），下载解压后整体移入 ~/.local/bin
mkdir -p ~/llama-vulkan && cd ~/llama-vulkan
curl -sL -o vulkan.tar.gz \
  "https://github.com/ggml-org/llama.cpp/releases/download/b12631/llama-b12631-bin-ubuntu-vulkan-x64.tar.gz"
tar xzf vulkan.tar.gz && mv llama-b12631/* ~/.local/bin/ && cd - && rm -rf ~/llama-vulkan
hash -r && llama-server --version   # 应显示 build 号
```

备选：源码编译（`cmake -B build -DGGML_VULKAN=ON && cmake --build build -j`，需 `vulkan-headers` 与 `shaderc`）。本机 RADV 驱动已就绪（`vulkaninfo` 可见 RENOIR 设备）。

**CPU 路线（备选）**：

```bash
sudo pacman -S llama-cpp
```

CPU 跑 0.6B 嵌入模型也是毫秒级，只是 `-ngl` 无效（GPU offload 失效）。

## 2. 下载模型并启动服务

### 2.1 下载 GGUF 权重

官方仓库 `Qwen/Qwen3-Embedding-0.6B-GGUF` 只有两个文件，二选一：

| 文件 | 大小 | 说明 |
|---|---|---|
| `Qwen3-Embedding-0.6B-Q8_0.gguf` | 610 MB | 推荐：质量损失极小，体积减半 |
| `Qwen3-Embedding-0.6B-f16.gguf` | 1142 MB | 完整精度（spec 原定版本） |

命令行下载到 `~/Downloads/`（支持断点续传；慢可把域名换成 `https://hf-mirror.com`）：

```bash
curl -L -C - -o ~/Downloads/Qwen3-Embedding-0.6B-Q8_0.gguf \
  "https://huggingface.co/Qwen/Qwen3-Embedding-0.6B-GGUF/resolve/main/Qwen3-Embedding-0.6B-Q8_0.gguf"
```

也可用浏览器打开仓库页面直接下载。embedding 模型的 GGUF 自包含 tokenizer，单文件即可，无需其他配套文件。

### 2.2 基于下载的模型启动（AMD 集显 GPU offload）

```bash
llama-server -m ~/Downloads/Qwen3-Embedding-0.6B-Q8_0.gguf \
  --embeddings --port 8081 --alias qwen3-embedding -ngl 999
```

- `-m`：加载本地 GGUF 文件（不联网）；换 f16 版本只需替换文件路径
- `-ngl 999`：全部层 offload 到 AMD 集显（Vulkan 后端，RADV/RENOIR）
- **显存**：集显为 UMA 共享内存，已分配 8GB——Q8_0（610 MB）或 f16（1142 MB）权重加 KV cache 远小于 8GB，无压力；集显"显存"就是系统内存，没有独显的容量与带宽限制
- `--embeddings`：只启用 embedding 模式（Qwen3-Embedding 使用 last-token pooling，llama.cpp 按 GGUF 元数据自动处理）
- `--alias qwen3-embedding`：给模型稳定名字，供 `/v1/embeddings` 请求的 `model` 字段使用
- 另有 `-hf Qwen/Qwen3-Embedding-0.6B-GGUF:F16` 自动下载模式，但主线按"先下载、后启动"操作，便于离线使用与版本固定

**offload 验证**：启动日志出现 `... assigned to device Vulkan0`（张量已分配到集显）且 `/v1/embeddings` 正常返回向量即成功。若日志完全无 Vulkan 字样，说明当前 llama-server 不是 Vulkan 构建，回到第 1 节换 Vulkan 版。

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
| 模型下载慢/中断 | 换 `https://hf-mirror.com` 域名重下；`curl -C -` 支持断点续传 |
| 启动日志无 Vulkan 字样 | llama-server 不是 Vulkan 构建，换第 1 节的 Vulkan 版 |
| llama-server 停了 | `memory_search` 静默回退 bigram，agent 不中断 |
| 中文检索效果 | Qwen3-Embedding 原生支持中文，无需分词配置 |
| 想换更大模型 | 下载对应 GGUF 后改 `-m` 路径，`OPENAI_EMBEDDINGS_MODEL` 同步改；冒烟校验自动重新把关 |

## 6. 升级路径备忘（暂不做）

sqlite FTS、全局记忆层、sleep-time 整理、RL 记忆策略——见 GLOSSARY「升级备忘」。
当前规模（32KB / 100 条）下余弦线性扫描已足够，向量库不是必需品。
