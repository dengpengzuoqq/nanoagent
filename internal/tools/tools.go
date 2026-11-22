// Package tools 是本地工具集：bash/read/write/edit/glob/grep 的定义与实现
// （计划模式的 enter_plan/submit_plan 在 internal/plan）。
package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"go-agent/internal/config"
)

// FunctionTool 构造 Responses API 的扁平结构工具定义
func FunctionTool(name, description string, parameters map[string]any) responses.ToolUnionParam {
	return responses.ToolUnionParam{
		OfFunction: &responses.FunctionToolParam{
			Name:        name,
			Description: openai.String(description),
			Parameters:  parameters,
		},
	}
}

// LocalTools 是本地执行工具集。
var LocalTools = []responses.ToolUnionParam{
	FunctionTool("bash", "Run shell command", map[string]any{
		"type":       "object",
		"properties": map[string]any{"command": map[string]any{"type": "string"}},
		"required":   []string{"command"},
	}),
	FunctionTool("read", "Read file with line numbers", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string"},
			"offset": map[string]any{"type": "integer"},
			"limit":  map[string]any{"type": "integer"},
		},
		"required": []string{"path"},
	}),
	FunctionTool("write", "Write content to file", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		},
		"required": []string{"path", "content"},
	}),
	FunctionTool("edit", "Replace string in file", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":       map[string]any{"type": "string"},
			"old_string": map[string]any{"type": "string"},
			"new_string": map[string]any{"type": "string"},
		},
		"required": []string{"path", "old_string", "new_string"},
	}),
	FunctionTool("glob", "Find files by pattern", map[string]any{
		"type":       "object",
		"properties": map[string]any{"pattern": map[string]any{"type": "string"}},
		"required":   []string{"pattern"},
	}),
	FunctionTool("grep", "Search files for pattern", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{"type": "string"},
			"path":    map[string]any{"type": "string"},
		},
		"required": []string{"pattern"},
	}),
}

// Read 读取文件并加 "%4d " 行号；offset 0 起始，limit 为行数，0 表示不限制。
func Read(path string, offset, limit int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	lines := strings.SplitAfter(string(data), "\n")
	// SplitAfter 在文件以 \n 结尾时多出一个尾部空元素，去掉
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	start := offset
	if start < 0 {
		start = 0
	}
	if start > len(lines) {
		return ""
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "%4d %s", i+1, lines[i])
	}
	return b.String()
}

// Edit 用 new_string 替换 old_string；old_string 必须恰好出现一次，否则报错且不改文件。
func Edit(path, oldString, newString string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	content := string(data)
	if strings.Count(content, oldString) != 1 {
		return "Error: old_string must appear exactly once"
	}
	if err := os.WriteFile(path, []byte(strings.Replace(content, oldString, newString, 1)), 0o644); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return "Successfully edited " + path
}

// Glob 按 pattern 找文件，mtime 降序；支持 ** 递归；无匹配返回提示。
func Glob(pattern string) string {
	var matches []string
	if strings.Contains(pattern, "**") {
		matches = globRecursive(pattern)
	} else {
		matches, _ = filepath.Glob(pattern)
	}
	if len(matches) == 0 {
		return "No files found"
	}
	// mtime 降序
	type mtimeItem struct {
		path string
		t    time.Time
	}
	items := make([]mtimeItem, 0, len(matches))
	for _, p := range matches {
		if st, err := os.Stat(p); err == nil {
			items = append(items, mtimeItem{p, st.ModTime()})
		} else {
			items = append(items, mtimeItem{p, time.Time{}})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].t.After(items[j].t) })
	paths := make([]string, len(items))
	for i, it := range items {
		paths[i] = it.path
	}
	return strings.Join(paths, "\n")
}

// globRecursive 实现 Python glob(recursive=True) 的 ** 语义：** 匹配零层或多层目录。
func globRecursive(pattern string) []string {
	segs := strings.Split(pattern, "/")
	dir := "."
	if strings.HasPrefix(pattern, "/") {
		dir = "/"
		segs = segs[1:]
	}
	var results []string
	var walk func(d string, segs []string)
	walk = func(d string, segs []string) {
		if len(segs) == 0 {
			return
		}
		if segs[0] == "**" {
			// ** 为尾段：Python glob(recursive=True) 返回目录下全部内容（含子目录）
			if len(segs) == 1 {
				collectAll(d, &results)
				return
			}
			walk(d, segs[1:]) // ** 匹配零层目录
			entries, err := os.ReadDir(d)
			if err != nil {
				return
			}
			for _, e := range entries {
				if e.IsDir() {
					walk(filepath.Join(d, e.Name()), segs) // ** 继续吃更深层目录
				}
			}
			return
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		last := len(segs) == 1
		for _, e := range entries {
			ok, _ := filepath.Match(segs[0], e.Name())
			if !ok {
				continue
			}
			p := filepath.Join(d, e.Name())
			if last {
				results = append(results, p)
			} else if e.IsDir() {
				walk(p, segs[1:])
			}
		}
	}
	walk(dir, segs)
	return results
}

// collectAll 递归收集目录下所有文件与子目录（** 作为尾段时的语义）。
func collectAll(dir string, results *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		*results = append(*results, p)
		if e.IsDir() {
			collectAll(p, results)
		}
	}
}

// Write 写文件，成功返回确认信息。
func Write(path, content string) string {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return "Successfully wrote to " + path
}

// Bash 执行 bash 命令，stdout 与 stderr 顺序拼接，
// 超时返回 Error 字符串；非零退出码不算错误。
func Bash(command string, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	// 命令留下后台子进程时，子进程仍握着 stdout/stderr 管道；WaitDelay 让 Wait 到点放手，
	// 否则超时杀掉的只是 bash 本身，agent 会一直等子进程退出
	cmd.WaitDelay = 2 * time.Second
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Sprintf("Error: command timed out after %s: %v", timeout, command)
	}
	// ErrWaitDelay：命令已结束但子进程还占着管道，已读到的输出照常返回
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		if _, ok := err.(*exec.ExitError); !ok {
			return fmt.Sprintf("Error: %v", err)
		}
	}
	config.Logger.Debug("execute_bash", "output_len", out.Len()+errb.Len())
	return out.String() + errb.String()
}

// Grep 调用系统 grep -r（参数数组传参，不走 shell，无注入面）；只回 stdout，无匹配返回提示。
func Grep(pattern, path string, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "grep", "-r", "--", pattern, path)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil && ctx.Err() != nil {
		return fmt.Sprintf("Error: grep timed out after %s", timeout)
	}
	if out.Len() == 0 {
		return "No matches found"
	}
	return out.String()
}
