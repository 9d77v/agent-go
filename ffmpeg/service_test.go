package ffmpeg

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// mockCfg 简易 ConfigProvider 用于测试
type mockCfg struct{ path string }

func (m *mockCfg) GetFFmpegPath() string { return m.path }
func (m *mockCfg) SetFFmpegPath(p string) error {
	m.path = p
	return nil
}

func TestSplitCommandLine(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`-y -i "C:\Users\93209\Pictures\Screenshots\屏幕截图 2026-08-02 143050.png" -c:v libwebp -quality 90 "C:\Temp\hhyimg-abc.webp"`,
			[]string{"-y", "-i", `C:\Users\93209\Pictures\Screenshots\屏幕截图 2026-08-02 143050.png`, "-c:v", "libwebp", "-quality", "90", `C:\Temp\hhyimg-abc.webp`}},
		{`-i input.mp4 out.gif`, []string{"-i", "input.mp4", "out.gif"}},
		{`-y -i ""`, []string{"-y", "-i"}},
		{`  -i   "a b"   `, []string{"-i", "a b"}},
	}
	for _, c := range cases {
		got := splitCommandLine(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("splitCommandLine(%q) = %#v, want %#v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("splitCommandLine(%q) = %#v, want %#v", c.in, got, c.want)
			}
		}
	}
}

// TestExecuteFFmpegChineseSpacePath 验证含中文+空格路径的 ffmpeg 转换能成功（回归 cmd /c 引号问题）。
func TestExecuteFFmpegChineseSpacePath(t *testing.T) {
	// 定位 ffmpeg（PATH / 常见路径）
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		// 常见安装路径兜底
		for _, p := range []string{`C:\ffmpeg\bin\ffmpeg.exe`, `/usr/bin/ffmpeg`, `/usr/local/bin/ffmpeg`} {
			if _, e := os.Stat(p); e == nil {
				ffmpegPath = p
				break
			}
		}
	}
	if ffmpegPath == "" {
		t.Skip("未检测到 ffmpeg，跳过集成测试")
	}

	// 构造含中文 + 空格的源路径
	srcName := "屏幕截图 2026-08-02 143050.png"
	src := filepath.Join(t.TempDir(), srcName)
	// 用 image/png 生成一张有效的 1x1 红色 PNG
	buf := &bytes.Buffer{}
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(buf, img); err != nil {
		t.Fatalf("生成测试 PNG 失败: %v", err)
	}
	if err := os.WriteFile(src, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("写源图失败: %v", err)
	}

	out := filepath.Join(t.TempDir(), "输出 结果.webp")

	svc := NewService(&mockCfg{path: ffmpegPath})
	cmdStr := `-y -i "` + src + `" -c:v libwebp -quality 90 "` + out + `"`
	output, err := svc.ExecuteFFmpeg(cmdStr)
	if err != nil {
		t.Fatalf("ExecuteFFmpeg 失败: %v\n输出: %s", err, output)
	}
	if _, e := os.Stat(out); e != nil {
		t.Fatalf("未生成输出文件 %s: %v", out, e)
	}
	if runtime.GOOS == "windows" {
		if strings.Contains(output, "Error opening input") {
			t.Fatalf("仍出现 Error opening input: %s", output)
		}
	}
}
