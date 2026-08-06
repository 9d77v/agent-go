// Package ffmpeg 提供 FFmpeg 检测、配置和执行的共享服务。
//
// 被 hhy-code 和 omnicraft-desktop 共同使用，避免跨项目重复代码。
package ffmpeg

import (
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

// ConfigProvider FFmpeg 配置存储接口
type ConfigProvider interface {
	GetFFmpegPath() string
	SetFFmpegPath(path string) error
}

// DetectResult ffmpeg 检测结果
type DetectResult struct {
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	Version string `json:"version"`
	Message string `json:"message"`
}

// Service 管理 FFmpeg 检测、配置和执行
type Service struct {
	cfg ConfigProvider
}

// NewService 创建共享 FFmpeg 服务
func NewService(cfg ConfigProvider) *Service {
	return &Service{cfg: cfg}
}

// DetectFFmpeg 检测系统是否存在 ffmpeg
// 搜索顺序：已配置路径 → PATH 环境变量 → 常见安装路径
func (s *Service) DetectFFmpeg() *DetectResult {
	// 1. 检查已配置路径
	configuredPath := s.cfg.GetFFmpegPath()
	if configuredPath != "" {
		if ver := checkFFmpegExecutable(configuredPath); ver != "" {
			return &DetectResult{
				Found: true, Path: configuredPath, Version: ver,
				Message: fmt.Sprintf("已配置路径: %s", configuredPath),
			}
		}
	}

	// 2. 检查 PATH 环境变量
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		if ver := checkFFmpegExecutable(path); ver != "" {
			_ = s.cfg.SetFFmpegPath(path)
			return &DetectResult{
				Found: true, Path: path, Version: ver,
				Message: fmt.Sprintf("在 PATH 中发现 ffmpeg: %s", path),
			}
		}
	}

	// 3. 检查常见安装路径
	for _, p := range commonFFmpegPaths() {
		if ver := checkFFmpegExecutable(p); ver != "" {
			_ = s.cfg.SetFFmpegPath(p)
			return &DetectResult{
				Found: true, Path: p, Version: ver,
				Message: fmt.Sprintf("在常见位置发现 ffmpeg: %s", p),
			}
		}
	}

	return &DetectResult{
		Found: false, Path: "", Version: "",
		Message: "未找到 ffmpeg，请手动设置路径或访问 ffmpeg.org 下载",
	}
}

// SetFFmpegPath 手动设置 ffmpeg 路径，验证可执行性后保存
func (s *Service) SetFFmpegPath(path string) error {
	if ver := checkFFmpegExecutable(path); ver == "" {
		return fmt.Errorf("无法验证 ffmpeg 可执行文件: %s", path)
	}
	return s.cfg.SetFFmpegPath(path)
}

// GetFFmpegPath 获取当前 ffmpeg 路径
func (s *Service) GetFFmpegPath() string {
	return s.cfg.GetFFmpegPath()
}

// GetFFmpegVersion 获取 ffmpeg 版本信息
func (s *Service) GetFFmpegVersion() string {
	path := s.cfg.GetFFmpegPath()
	if path == "" {
		return ""
	}
	return checkFFmpegExecutable(path)
}

// ExecuteFFmpeg 执行 ffmpeg 命令
// command 是不包含 "ffmpeg" 前缀的参数字符串，例如 `-y -i "input path" -c:v libwebp "out.webp"`。
// 直接以独立参数启动 ffmpeg，而非经由 cmd /c / sh -c，避免 shell 对含空格/非 ASCII 路径的引号解析问题。
func (s *Service) ExecuteFFmpeg(command string) (string, error) {
	ffmpegPath := s.cfg.GetFFmpegPath()
	if ffmpegPath == "" {
		return "", fmt.Errorf("ffmpeg 路径未配置")
	}

	log.Printf("[FFmpeg] 执行: %s %s", ffmpegPath, command)

	cmd := exec.Command(ffmpegPath, splitCommandLine(command)...)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	if err != nil {
		return output, fmt.Errorf("ffmpeg 执行失败: %w\n%s", err, stderr.String())
	}
	return output, nil
}

// splitCommandLine 将命令参数字符串按空白拆分为参数列表，尊重双引号包裹（引号内空白保留、引号本身剥除）。
// 用于绕过 Windows cmd /c 的引号解析缺陷，改用 exec.Command 直接传参（由系统负责正确的引号转义）。
func splitCommandLine(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			args = append(args, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == ' ' || c == '\t':
			if inQuote {
				cur.WriteByte(c)
			} else {
				flush()
			}
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return args
}

// GetFFmpegWebsiteURL 返回 ffmpeg 官网下载地址
func (s *Service) GetFFmpegWebsiteURL() string {
	return "https://ffmpeg.org/download.html"
}

// checkFFmpegExecutable 验证 ffmpeg 可执行文件并返回版本信息
func checkFFmpegExecutable(path string) string {
	cmd := exec.Command(path, "-version")
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return ""
	}

	// 提取第一行作为版本信息
	lines := strings.SplitN(out.String(), "\n", 2)
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return ""
}

// commonFFmpegPaths 返回各平台常见的 ffmpeg 安装路径
func commonFFmpegPaths() []string {
	if runtime.GOOS == "windows" {
		return []string{
			"C:\\ffmpeg\\bin\\ffmpeg.exe",
			"C:\\Program Files\\ffmpeg\\bin\\ffmpeg.exe",
			"C:\\Program Files (x86)\\ffmpeg\\bin\\ffmpeg.exe",
			".\\ffmpeg.exe",
			"ffmpeg.exe",
			".\\ffmpeg\\bin\\ffmpeg.exe",
		}
	}
	return []string{
		"/usr/local/bin/ffmpeg",
		"/usr/bin/ffmpeg",
		"/opt/homebrew/bin/ffmpeg",
	}
}
