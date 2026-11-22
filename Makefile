# nanoAgent：Go 版 agent 的构建/测试/运行入口
# 布局：cmd/nanoagent（入口）+ internal/{agent,config,prompt,tools,plan,mcp}
# 注意：运行必须在项目根目录（依赖 ./test.env 与 ./nanoagents.md 的相对路径）

AGENT   := agent

.PHONY: build test vet fmt clean run all

all: build

build:
	go build -o $(AGENT) ./cmd/nanoagent

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

clean:
	rm -f $(AGENT)

# 用法：make run ARGS='--max-iter 8 "任务内容"'
run: build
	./$(AGENT) $(ARGS)
