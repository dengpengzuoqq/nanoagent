# nanoAgent：Go 版 agent 的构建/测试/运行入口
# 布局：cmd/nanoagent（入口）+ internal/{agent,config,prompt,tools,plan,mcp}
# 注意：运行 fixture（test.env、nanoagents.md、.nanoagents）在 debug/ 目录

AGENT   := agent

# 编译模式：make build MODE=debug|release（默认 release）
# debug   = -gcflags "all=-N -l"：关内联与优化，支持 delve 断点调试；产物放 debug/
# release = -ldflags "-s -w"：去符号表与 DWARF，产物更小；产物放 release/
MODE    ?= release

ifeq ($(MODE),debug)
BUILD_FLAGS := -gcflags "all=-N -l"
OUT_DIR     := debug/
else
BUILD_FLAGS := -ldflags "-s -w"
OUT_DIR     := release/
endif

.PHONY: build test vet fmt clean run embedding all

all: build

build:
	@mkdir -p $(OUT_DIR)
	go build $(BUILD_FLAGS) -o $(OUT_DIR)$(AGENT) ./cmd/nanoagent

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

clean:
	rm -rf debug/agent release/agent

# 用法：make embedding   —— 输出用 llama-server 启动本地嵌入服务的命令（不执行）
embedding:
	@echo "llama-server -m ~/Downloads/Qwen3-Embedding-0.6B-f16.gguf \\"
	@echo "  --embedding --port 8081 --alias qwen3-embedding \\"
	@echo "  -ngl 999 -b 512 -ub 512 -c 2048 -np 1 --api-key local"

# 用法：make run ARGS='--max-iter 8 "任务内容"'
# debug 模式在 debug/ 下运行（fixture 所在目录）；release 模式在 release/ 下运行
run: build embedding
	cd $(OUT_DIR) && ./$(AGENT) $(ARGS)
