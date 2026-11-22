package tools

import (
	"os"
	"strings"
	"testing"
	"time"
)

// toolRead：行号 "%4d " 前缀；offset 0 起始，limit 为读取行数。
func TestToolRead(t *testing.T) {
	path := t.TempDir() + "/f.txt"
	os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644)
	cases := []struct {
		name          string
		offset, limit int
		want          string
	}{
		{"whole file", 0, 0, "   1 alpha\n   2 beta\n   3 gamma\n"},
		{"offset 1 limit 1", 1, 1, "   2 beta\n"},
		{"offset 2 no limit", 2, 0, "   3 gamma\n"},
		{"offset beyond eof", 10, 0, ""},
		{"limit truncates", 0, 2, "   1 alpha\n   2 beta\n"},
	}
	for _, tc := range cases {
		got, err := Read(path, tc.offset, tc.limit)
		if err != nil {
			t.Errorf("%s: Read 意外错误: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: toolRead = %q, want %q", tc.name, got, tc.want)
		}
	}
	if _, err := Read(path+".missing", 0, 0); err == nil {
		t.Error("Read(missing) 应返回错误")
	}
}

// toolEdit：old_string 必须恰好出现一次（0 次或多次都报错不改文件）。
func TestToolEdit(t *testing.T) {
	path := t.TempDir() + "/f.txt"
	os.WriteFile(path, []byte("hello world\n"), 0o644)
	got, err := Edit(path, "world", "Go")
	if err != nil || got != "Successfully edited "+path {
		t.Errorf("toolEdit ok case = %q, err=%v", got, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello Go\n" {
		t.Errorf("替换结果 = %q", data)
	}
	if _, err := Edit(path, "absent", "x"); !strings.Contains(err.Error(), "old_string must appear exactly once") {
		t.Errorf("toolEdit 0 次匹配 = %v", err)
	}
	if _, err := Edit(path, "o", "0"); !strings.Contains(err.Error(), "old_string must appear exactly once") {
		t.Errorf("toolEdit 多次匹配 = %v", err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "hello Go\n" {
		t.Error("失败的 edit 不应改文件")
	}
}

// toolGlob：pattern 匹配（支持 ** 递归），mtime 降序；无匹配返回 "No files found"。
func TestToolGlob(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(root+"/sub/deep", 0o755)
	os.WriteFile(root+"/a.py", []byte("a"), 0o644)
	os.WriteFile(root+"/sub/b.py", []byte("b"), 0o644)
	os.WriteFile(root+"/sub/deep/c.py", []byte("c"), 0o644)
	os.WriteFile(root+"/note.txt", []byte("n"), 0o644)
	// 固定 mtime：a 最旧，c 最新
	base := time.Unix(1700000000, 0)
	os.Chtimes(root+"/a.py", base, base)
	os.Chtimes(root+"/sub/b.py", base, base.Add(time.Hour))
	os.Chtimes(root+"/sub/deep/c.py", base, base.Add(2*time.Hour))

	cases := []struct {
		name, pattern, want string
	}{
		{"recursive", root + "/**/*.py", root + "/sub/deep/c.py\n" + root + "/sub/b.py\n" + root + "/a.py"},
		{"single level", root + "/sub/*.py", root + "/sub/b.py"},
		{"top only", root + "/*.py", root + "/a.py"},
		{"no match", root + "/**/*.md", "No files found"},
	}
	for _, tc := range cases {
		got, err := Glob(tc.pattern)
		if err != nil {
			t.Errorf("%s: Glob 意外错误: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: Glob(%q) = %q, want %q", tc.name, tc.pattern, got, tc.want)
		}
	}
}

// ** 作为尾段（dir/**）：返回目录下全部文件与子目录。
func TestToolGlobTailStarStar(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(root+"/sub/deep", 0o755)
	os.WriteFile(root+"/a.py", []byte("a"), 0o644)
	os.WriteFile(root+"/sub/b.py", []byte("b"), 0o644)
	got, err := Glob(root + "/**")
	if err != nil {
		t.Fatalf("Glob 意外错误: %v", err)
	}
	for _, want := range []string{root + "/a.py", root + "/sub", root + "/sub/b.py", root + "/sub/deep"} {
		if !strings.Contains(got, want) {
			t.Errorf("Glob(tail **) 缺少 %s，实际: %q", want, got)
		}
	}
}

// toolWrite：写入并返回成功信息；错误返回 error。
func TestToolWrite(t *testing.T) {
	path := t.TempDir() + "/out.txt"
	got, err := Write(path, "content-1")
	if err != nil || got != "Successfully wrote to "+path {
		t.Errorf("toolWrite = %q, err=%v", got, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "content-1" {
		t.Errorf("内容 = %q", data)
	}
	if _, err := Write(t.TempDir()+"/no-such-dir/x", "y"); err == nil {
		t.Error("toolWrite 坏路径应返回错误")
	}
}

// toolBash：stdout 与 stderr 顺序拼接，超时返回 error，退出码不影响输出。
func TestToolBash(t *testing.T) {
	got, err := Bash("echo hello", 10*time.Second)
	if err != nil || got != "hello\n" {
		t.Errorf("toolBash echo = %q, err=%v", got, err)
	}
	got, err = Bash("echo out; echo err 1>&2", 10*time.Second)
	if err != nil || got != "out\nerr\n" {
		t.Errorf("toolBash stderr = %q, err=%v", got, err)
	}
	got, err = Bash("echo hi; exit 3", 10*time.Second)
	if err != nil || got != "hi\n" {
		t.Errorf("toolBash 非零退出码 = %q, err=%v", got, err)
	}
	if _, err := Bash("sleep 5", 300*time.Millisecond); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("toolBash 超时 = %v", err)
	}
	// 后台子进程仍握着 stdout 管道时，超时也必须生效（否则 agent 会一直等子进程退出）
	start := time.Now()
	if _, err := Bash("sleep 5 & echo started", 300*time.Millisecond); err == nil {
		t.Error("toolBash 后台子进程超时应返回错误")
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("后台子进程超时耗时 %v，应远小于子进程自身寿命", d)
	}
}

// toolGrep：exec grep（不走 shell），只回 stdout；无匹配返回 "No matches found"。
func TestToolGrep(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(root+"/x.txt", []byte("needle here\nother\n"), 0o644)
	got, err := Grep("needle", root, 10*time.Second)
	if err != nil || !strings.Contains(got, "needle here") {
		t.Errorf("toolGrep = %q, err=%v", got, err)
	}
	got, err = Grep("zzz-not-there", root, 10*time.Second)
	if err != nil || got != "No matches found" {
		t.Errorf("toolGrep 无匹配 = %q, err=%v", got, err)
	}
	// path 为空默认当前目录：在包目录内 grep "Package" 必有匹配（package tools 声明）
	got, err = Grep("package tools", "", 10*time.Second)
	if err != nil || !strings.Contains(got, "package tools") {
		t.Errorf("toolGrep 默认目录 = %q, err=%v", got, err)
	}
}
