package agent

import "github.com/9d77v/agent-go/tool"

// ImageInput 随请求提交的图片（前端已转 webp 并上传 OSS，仅传 URL）。
type ImageInput struct {
	URL string `json:"url,omitempty"`
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
