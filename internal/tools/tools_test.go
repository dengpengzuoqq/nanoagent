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
		if got := Read(path, tc.offset, tc.limit); got != tc.want {
			t.Errorf("%s: toolRead = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := Read(path+".missing", 0, 0); !strings.HasPrefix(got, "Error:") {
		t.Errorf("Read(missing) = %q, want Error prefix", got)
	}
}

// toolEdit：old_string 必须恰好出现一次（0 次或多次都报错不改文件）。
func TestToolEdit(t *testing.T) {
	path := t.TempDir() + "/f.txt"
	os.WriteFile(path, []byte("hello world\n"), 0o644)
	if got := Edit(path, "world", "Go"); got != "Successfully edited "+path {
		t.Errorf("toolEdit ok case = %q", got)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello Go\n" {
		t.Errorf("替换结果 = %q", data)
	}
	if got := Edit(path, "absent", "x"); !strings.HasPrefix(got, "Error: old_string must appear exactly once") {
		t.Errorf("toolEdit 0 次匹配 = %q", got)
	}
	if got := Edit(path, "o", "0"); !strings.HasPrefix(got, "Error: old_string must appear exactly once") {
		t.Errorf("toolEdit 多次匹配 = %q", got)
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
		if got := Glob(tc.pattern); got != tc.want {
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
	got := Glob(root + "/**")
	for _, want := range []string{root + "/a.py", root + "/sub", root + "/sub/b.py", root + "/sub/deep"} {
		if !strings.Contains(got, want) {
			t.Errorf("Glob(tail **) 缺少 %s，实际: %q", want, got)
		}
	}
}

// toolWrite：写入并返回成功信息；错误返回 Error 前缀。
func TestToolWrite(t *testing.T) {
	path := t.TempDir() + "/out.txt"
	if got := Write(path, "content-1"); got != "Successfully wrote to "+path {
		t.Errorf("toolWrite = %q", got)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "content-1" {
		t.Errorf("内容 = %q", data)
	}
	if got := Write(t.TempDir()+"/no-such-dir/x", "y"); !strings.HasPrefix(got, "Error:") {
		t.Errorf("toolWrite 坏路径 = %q", got)
	}
}

// toolBash：stdout 与 stderr 顺序拼接，超时返回 Error，退出码不影响输出。
func TestToolBash(t *testing.T) {
	if got := Bash("echo hello", 10*time.Second); got != "hello\n" {
		t.Errorf("toolBash echo = %q", got)
	}
	if got := Bash("echo out; echo err 1>&2", 10*time.Second); got != "out\nerr\n" {
		t.Errorf("toolBash stderr = %q", got)
	}
	if got := Bash("echo hi; exit 3", 10*time.Second); got != "hi\n" {
		t.Errorf("toolBash 非零退出码 = %q", got)
	}
	if got := Bash("sleep 5", 300*time.Millisecond); !strings.HasPrefix(got, "Error:") {
		t.Errorf("toolBash 超时 = %q", got)
	}
	// 后台子进程仍握着 stdout 管道时，超时也必须生效（否则 agent 会一直等子进程退出）
	start := time.Now()
	if got := Bash("sleep 5 & echo started", 300*time.Millisecond); !strings.HasPrefix(got, "Error:") {
		t.Errorf("toolBash 后台子进程超时 = %q", got)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("后台子进程超时耗时 %v，应远小于子进程自身寿命", d)
	}
}

// toolGrep：exec grep（不走 shell），只回 stdout；无匹配返回 "No matches found"。
func TestToolGrep(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(root+"/x.txt", []byte("needle here\nother\n"), 0o644)
	got := Grep("needle", root, 10*time.Second)
	if !strings.Contains(got, "needle here") {
		t.Errorf("toolGrep = %q", got)
	}
	if got := Grep("zzz-not-there", root, 10*time.Second); got != "No matches found" {
		t.Errorf("toolGrep 无匹配 = %q", got)
	}
}
