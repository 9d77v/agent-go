package tool

import (
	"context"
	"encoding/json"
)

// ToolResult 工具执行结果
type ToolResult struct {
	Success    bool            `json:"success"`
	Output     string          `json:"output,omitempty"`
	Error      string          `json:"error,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
	Diff       string          `json:"diff,omitempty"`
	BackupPath string          `json:"backup_path,omitempty"`
	FileExists bool            `json:"file_exists,omitempty"`
}

// ToolHandler 单个工具的执行函数类型。
// ctx 携带当前调用上下文（ADK agent.Context，含 SessionID()），供按调用获取会话身份。
type ToolHandler func(ctx context.Context, args json.RawMessage) *ToolResult

// ---------- 风险级别 ----------

type RiskLevel string

const (
	RiskSafe      RiskLevel = "safe"
	RiskModerate  RiskLevel = "moderate"
	RiskDangerous RiskLevel = "dangerous"
)

// ---------- 审批模式 ----------

type ApprovalMode string

const (
	ApprovalModeAuto    ApprovalMode = "auto"
	ApprovalModeDefault ApprovalMode = "default"
	ApprovalModeManual  ApprovalMode = "manual"
)

func ParseApprovalMode(s string) ApprovalMode {
	switch s {
	case "auto":
		return ApprovalModeAuto
	case "manual":
		return ApprovalModeManual
	default:
		return ApprovalModeDefault
	}
}

// ---------- 子任务委托 ----------

type SubTaskDelegate func(ctx context.Context, description string, filePaths []string, workspaceRoot string) *SubTaskResult

type SubTaskResult struct {
	Success      bool     `json:"success"`
	Summary      string   `json:"summary"`
	FilesChanged []string `json:"files_changed"`
	Error        string   `json:"error,omitempty"`
}

// ---------- Token 用量 ----------

type TokenUsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	ContextWindow    int `json:"context_window"`
}

// ---------- 工具参数类型 ----------

type ReadFileArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
}

type WriteFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type EditFileArgs struct {
	Path  string          `json:"path"`
	Edits json.RawMessage `json:"edits"`
}

type FileSearchArgs struct {
	Glob string `json:"glob"`
}

type GrepSearchArgs struct {
	Query          string `json:"query"`
	IsRegexp       bool   `json:"isRegexp,omitempty"`
	IncludePattern string `json:"includePattern,omitempty"`
}

type ListDirArgs struct {
	Path string `json:"path"`
}

type RunCommandArgs struct {
	Command string `json:"command"`
}

type MemoryArgs struct {
	Action  string `json:"action"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
}

type AskQuestionsArgs struct {
	Questions []QuestionItem `json:"questions"`
}

type QuestionItem struct {
	Header string `json:"header"`
	Answer string `json:"answer"`
}

type NewWorkspaceArgs struct {
	Description string `json:"description"`
	Template    string `json:"template,omitempty"`
}

type DelegateTaskArgs struct {
	Description string   `json:"description"`
	FilePaths   []string `json:"filePaths"`
}

// ToolDefinition 工具定义（供 LLM function calling 使用）
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  any
}
