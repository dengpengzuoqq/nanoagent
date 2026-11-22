package config

import (
	"os"
	"reflect"
	"testing"
)

// TestLoadEnv：loadEnv 的 KEY=VALUE 解析（引号剥离、# 注释、无等号行跳过）。
func TestLoadEnv(t *testing.T) {
	path := t.TempDir() + "/env"
	content := "# comment\nOPENAI_API_KEY='sk-abc'\nOPENAI_MODEL = \"m1\"\nBROKEN_LINE\nKEY2=v2 # trailing\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := loadEnv(path)
	want := map[string]string{"OPENAI_API_KEY": "sk-abc", "OPENAI_MODEL": "m1", "KEY2": "v2 # trailing"}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("loadEnv = %v, want %v", cfg, want)
	}
	// 缺失文件：返回空 map 而非退出
	if cfg := loadEnv(path + ".missing"); len(cfg) != 0 {
		t.Errorf("loadEnv(missing) = %v, want empty", cfg)
	}
}
