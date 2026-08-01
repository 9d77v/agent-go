package agent

import "github.com/9d77v/agent-go/tool"

// AgentErrorType 错误类型
type AgentErrorType string

const (
	AgentErrNone        AgentErrorType = ""
	AgentErrCompileFail AgentErrorType = "compile_fail"
	AgentErrToolFail    AgentErrorType = "tool_fail"
)

// ImageInput 随请求提交的图片（已转换为 webp 的 base64 数据）。
// Data 字段为 base64 编码的图片字节，Go JSON 反序列化时自动解码为 []byte。
type ImageInput struct {
	MIME string `json:"mime,omitempty"`
	Data []byte `json:"data,omitempty"`
}

// QuestionnaireAnswer 用户对问卷的回答
type QuestionnaireAnswer struct {
	Answers []QuestionItem `json:"answers"`
}

// QuestionItem 单个问题的回答
type QuestionItem struct {
	Header string `json:"header"`
	Answer string `json:"answer"`
}

// ApprovalResult 审批结果
type ApprovalResult struct {
	Approved bool
	Reason   string
}

// PendingCall 待执行的工具调用
type PendingCall struct {
	Name string
	Args string
	ID   string
}

// AgentErrorState 错误状态
type AgentErrorState struct {
	ErrType     AgentErrorType
	File        string
	Message     string
	Retries     int
	LastAttempt string
}

// ---------- Orchestrator 回调 ----------

// OrchestratorCallbacks 编排器 → 流式通信层的回调接口
type OrchestratorCallbacks struct {
	OnMessageStart     func(msgID string, seq int64, turnID, role string)
	OnContentDelta     func(msgID, delta string)
	OnReasoningDelta   func(msgID, delta string)
	OnMessageEnd       func(msgID string)
	OnToolCallStart    func(msgID, callID, name string)
	OnToolCallDelta    func(callID, argsDelta string)
	OnToolCallEnd      func(callID, name, arguments string)
	OnToolExecuting    func(callID, name string)
	OnToolResult       func(callID, toolMsgID string, result *tool.ToolResult)
	OnApprovalRequired func(approvalID, callID, command string, risk tool.RiskLevel)
	OnQuestionnaire    func(questionnaireID, questionsJSON string)
	OnTokenUsage       func(usage tool.TokenUsageInfo)
	OnTurnComplete     func(turnID, sessionID string)
	OnError            func(msgID, code, message string)
	OnTerminalOutput   func(text string, isStderr bool)
}

// ---------- 编排器配置 ----------

// OrchestratorConfig 编排器配置
type OrchestratorConfig struct {
	IterationsPerBatch int
	ContextWindow      int
	OutputReserve      int
	WorkspaceID        string
	MaxErrorRetries    int
}
