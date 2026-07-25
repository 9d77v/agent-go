package agent

// AgentErrorType 错误类型
type AgentErrorType string

const (
	AgentErrNone        AgentErrorType = ""
	AgentErrCompileFail AgentErrorType = "compile_fail"
	AgentErrToolFail    AgentErrorType = "tool_fail"
)

// QuestionnaireAnswer 用户对问卷的回答
type QuestionnaireAnswer struct {
	Answers []QuestionItem `json:"answers"`
}

// QuestionItem 单个问题的回答
type QuestionItem struct {
	Header string `json:"header"`
	Answer string `json:"answer"`
}

// AgentFileChange 单个文件变更记录
type AgentFileChange struct {
	Path       string `json:"path"`
	BackupPath string `json:"backup_path"`
	Status     string `json:"status"`
	Timestamp  int64  `json:"timestamp"`
}

type changeRecord struct {
	change   AgentFileChange
	absPath  string
	reverted bool
	accepted bool
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
