package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/9d77v/agent-go/llm"
	"github.com/9d77v/agent-go/msgdb"
	ftool "github.com/9d77v/agent-go/tool"
)

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
	OnToolResult       func(callID, toolMsgID string, result *ftool.ToolResult)
	OnFileDiff         func(toolMsgID, filePath, original, modified, backupPath string)
	OnApprovalRequired func(approvalID, callID, command string, risk ftool.RiskLevel)
	OnQuestionnaire    func(questionnaireID, questionsJSON string)
	OnTokenUsage       func(usage ftool.TokenUsageInfo)
	OnTurnComplete     func(turnID, sessionID string)
	OnError            func(msgID, code, message string)
	OnTerminalOutput   func(text string, isStderr bool)
}

// OrchestratorConfig 编排器配置
type OrchestratorConfig struct {
	IterationsPerBatch int
	ContextWindow      int
	OutputReserve      int
	WorkspaceID        string
	MaxErrorRetries    int // 最大错误重试次数，默认 3
	// TitleGenerator 自定义标题生成函数。若未设置，使用默认 LLM 调用生成英文标题
	TitleGenerator func(ctx context.Context, userMessage, model, providerID string) string
}

// OrchestratorHooks 项目层注入的钩子函数
type OrchestratorHooks struct {
	GetMessages           func(sessionID string, userMessage, mode string) []llm.ChatMessage
	OnToolResult          func(ctx context.Context, sessionID, turnID string, approvalMode ftool.ApprovalMode, pc PendingCall, callbacks *OrchestratorCallbacks, es **AgentErrorState) ([]llm.ChatMessage, bool)
	OnAfterToolExec       func(toolName string, result *ftool.ToolResult, filePath string)
	OnBeforeSystemContext func() string
	OnAfterTurn           func(sessionID string, turnID string)
}

// Orchestrator 通用 Agent 编排器
type Orchestrator struct {
	llmSvc          LLMService
	toolExec        ToolExecutor
	promptBuilder   SystemPromptBuilder
	toolDefProvider ToolDefinitionProvider
	db              *msgdb.Store
	config          OrchestratorConfig
	hooks           OrchestratorHooks

	pendingApprovals      map[string]chan ApprovalResult
	approvalMu            sync.Mutex
	pendingQuestionnaires map[string]chan string
	questionnaireMu       sync.Mutex

	onSubApprovalRequest func(approvalID, command string, riskLevel ftool.RiskLevel)
}

// NewOrchestrator 创建通用编排器
func NewOrchestrator(
	llmSvc LLMService,
	toolExec ToolExecutor,
	promptBuilder SystemPromptBuilder,
	toolDefProvider ToolDefinitionProvider,
	db *msgdb.Store,
	config OrchestratorConfig,
	hooks OrchestratorHooks,
) *Orchestrator {
	return &Orchestrator{
		llmSvc:                llmSvc,
		toolExec:              toolExec,
		promptBuilder:         promptBuilder,
		toolDefProvider:       toolDefProvider,
		db:                    db,
		config:                config,
		hooks:                 hooks,
		pendingApprovals:      make(map[string]chan ApprovalResult),
		pendingQuestionnaires: make(map[string]chan string),
	}
}

// SetSubApprovalCallback 设置子 Agent 审批请求回调
func (o *Orchestrator) SetSubApprovalCallback(cb func(approvalID, command string, riskLevel ftool.RiskLevel)) {
	o.onSubApprovalRequest = cb
}

// RegisterApproval 注册审批等待
func (o *Orchestrator) RegisterApproval(approvalID string) chan ApprovalResult {
	ch := make(chan ApprovalResult, 1)
	o.approvalMu.Lock()
	o.pendingApprovals[approvalID] = ch
	o.approvalMu.Unlock()
	return ch
}

// ResolveApproval 完成审批
func (o *Orchestrator) ResolveApproval(approvalID string, approved bool) {
	o.approvalMu.Lock()
	ch, ok := o.pendingApprovals[approvalID]
	delete(o.pendingApprovals, approvalID)
	o.approvalMu.Unlock()
	if ok {
		ch <- ApprovalResult{Approved: approved}
	}
}

// ResumeAfterApproval 审批后恢复执行
func (o *Orchestrator) ResumeAfterApproval(approvalID string, approved bool) {
	o.ResolveApproval(approvalID, approved)
}

// RegisterQuestionnaire 注册问卷等待
func (o *Orchestrator) RegisterQuestionnaire(questionnaireID string) chan string {
	ch := make(chan string, 1)
	o.questionnaireMu.Lock()
	o.pendingQuestionnaires[questionnaireID] = ch
	o.questionnaireMu.Unlock()
	return ch
}

// ResolveQuestionnaire 完成问卷
func (o *Orchestrator) ResolveQuestionnaire(questionnaireID string, answersJSON string) {
	o.questionnaireMu.Lock()
	ch, ok := o.pendingQuestionnaires[questionnaireID]
	delete(o.pendingQuestionnaires, questionnaireID)
	o.questionnaireMu.Unlock()
	if ok {
		ch <- answersJSON
	}
}

func (o *Orchestrator) waitForApproval(ctx context.Context, approvalID string) bool {
	approvalCh := o.RegisterApproval(approvalID)
	select {
	case result := <-approvalCh:
		return result.Approved
	case <-ctx.Done():
		return false
	case <-time.After(5 * time.Minute):
		return false
	}
}

func genID(prefix string) string {
	return fmt.Sprintf("%s_%s_%d", prefix, uuid.Must(uuid.NewV7()).String(), time.Now().UnixNano())
}

// Orchestrate 执行完整的 Agent 多轮对话
func (o *Orchestrator) Orchestrate(
	ctx context.Context,
	sessionID, message, model, providerID, mode, thinking string,
	approvalMode ftool.ApprovalMode,
	callbacks *OrchestratorCallbacks,
) {
	log.Printf("[Orchestrator] 开始编排 [session=%s, mode=%s, approval=%s]", sessionID, mode, approvalMode)

	isNewSession := sessionID == ""
	if isNewSession {
		sessionID = genID("session")
	}

	turnID := genID("turn")

	if isNewSession {
		title := o.generateTitle(ctx, message, model, providerID)
		if title == "" {
			title = message
			if len(title) > 50 {
				title = title[:50] + "..."
			}
		}
		o.db.CreateSession(sessionID, o.config.WorkspaceID)
		o.db.UpdateSessionTitle(sessionID, title)
	}

	baseMessages := o.hooks.GetMessages(sessionID, message, mode)

	userMsg := &msgdb.Message{
		ID:        genID("msg"),
		SessionID: sessionID,
		TurnID:    turnID,
		Role:      "user",
		Content:   message,
	}
	if err := o.db.InsertMessage(userMsg); err != nil {
		log.Printf("[Orchestrator] 持久化用户消息失败: %v", err)
	}
	o.db.UpdateLLMLastSentMsgID(sessionID, userMsg.ID)

	iterationsPerBatch := o.config.IterationsPerBatch
	if iterationsPerBatch <= 0 {
		iterationsPerBatch = 50
	}
	batchIteration := 0
	var errorState *AgentErrorState

	contextWindow := o.config.ContextWindow
	outputReserve := o.config.OutputReserve
	promptTokensLatest := 0

	for {
		select {
		case <-ctx.Done():
			if callbacks.OnError != nil {
				callbacks.OnError("", "cancelled", ctx.Err().Error())
			}
			return
		default:
		}

		if batchIteration >= iterationsPerBatch {
			continueID := genID("cnt")
			if callbacks.OnApprovalRequired != nil {
				callbacks.OnApprovalRequired(continueID, "", fmt.Sprintf("已执行 %d/%d 轮工具调用，是否继续？", batchIteration, iterationsPerBatch), ftool.RiskSafe)
			}
			approved := o.waitForApproval(ctx, continueID)
			if !approved {
				summaryMsg := fmt.Sprintf("任务已暂停，已执行 %d 轮工具调用。", batchIteration)
				assistantMsg := &msgdb.Message{
					ID:        genID("msg"),
					SessionID: sessionID,
					TurnID:    turnID,
					Role:      "assistant",
					Content:   summaryMsg,
				}
				o.db.InsertMessage(assistantMsg)
				if callbacks.OnTurnComplete != nil {
					callbacks.OnTurnComplete(turnID, sessionID)
				}
				return
			}
			batchIteration = 0
			baseMessages = append(baseMessages, llm.ChatMessage{
				Role:    "system",
				Content: "用户已确认继续执行，请继续完成剩余任务。",
			})
			continue
		}

		if contextWindow > 0 && promptTokensLatest+outputReserve > contextWindow {
			limitMsg := fmt.Sprintf("⚠️ 已达到模型上下文窗口上限（已用 %d / %d tokens）。", promptTokensLatest, contextWindow)
			assistantMsg := &msgdb.Message{
				ID:        genID("msg"),
				SessionID: sessionID,
				TurnID:    turnID,
				Role:      "assistant",
				Content:   limitMsg,
			}
			o.db.InsertMessage(assistantMsg)
			if callbacks.OnError != nil {
				callbacks.OnError(assistantMsg.ID, "context_full", limitMsg)
			}
			return
		}

		llmReq := &llm.ChatRequest{
			ProviderID:    providerID,
			Model:         model,
			Messages:      baseMessages,
			Temperature:   0.3,
			Thinking:      thinking,
			Tools:         mode == "agent",
			ContextWindow: contextWindow,
		}

		dbAssistantMsg := &msgdb.Message{
			ID:        genID("msg"),
			SessionID: sessionID,
			TurnID:    turnID,
			Role:      "assistant",
		}
		if err := o.db.InsertMessage(dbAssistantMsg); err != nil {
			log.Printf("[Orchestrator] 持久化 assistant 消息失败: %v", err)
		}
		if callbacks.OnMessageStart != nil {
			callbacks.OnMessageStart(dbAssistantMsg.ID, 0, turnID, "assistant")
		}

		var chatResp *llm.ChatResponse
		var llmErr error
		var pendingCalls []PendingCall

		chatResp, llmErr = o.llmSvc.ChatStream(ctx, llmReq,
			func(delta string) {
				if callbacks.OnContentDelta != nil {
					callbacks.OnContentDelta(dbAssistantMsg.ID, delta)
				}
			},
			func(delta string) {
				if callbacks.OnReasoningDelta != nil {
					callbacks.OnReasoningDelta(dbAssistantMsg.ID, delta)
				}
			},
			func(callID, name string) {
				if callbacks.OnToolCallStart != nil {
					callbacks.OnToolCallStart(dbAssistantMsg.ID, callID, name)
				}
			},
			func(callID, argsDelta string) {
				if callbacks.OnToolCallDelta != nil {
					callbacks.OnToolCallDelta(callID, argsDelta)
				}
			},
			func(callID, name, arguments string) {
				pendingCalls = append(pendingCalls, PendingCall{Name: name, Args: arguments, ID: callID})
				if callbacks.OnToolCallEnd != nil {
					callbacks.OnToolCallEnd(callID, name, arguments)
				}
			},
		)

		if llmErr != nil {
			log.Printf("[Orchestrator] LLM 调用失败: %v", llmErr)
			if callbacks.OnError != nil {
				callbacks.OnError(dbAssistantMsg.ID, "llm_error", llmErr.Error())
			}
			errContent := fmt.Sprintf("⚠️ LLM 调用失败: %v", llmErr)
			dbAssistantMsg.Content = errContent
			o.db.InsertMessage(dbAssistantMsg)
			if callbacks.OnMessageEnd != nil {
				callbacks.OnMessageEnd(dbAssistantMsg.ID)
			}
			return
		}

		dbAssistantMsg.Content = chatResp.Content
		dbAssistantMsg.Reasoning = chatResp.ReasoningContent
		o.db.InsertMessage(dbAssistantMsg)
		if callbacks.OnMessageEnd != nil {
			callbacks.OnMessageEnd(dbAssistantMsg.ID)
		}

		if contextWindow > 0 && chatResp.Usage != nil {
			promptTokensLatest = chatResp.Usage.PromptTokens
			usage := ftool.TokenUsageInfo{
				PromptTokens:     chatResp.Usage.PromptTokens,
				CompletionTokens: chatResp.Usage.CompletionTokens,
				TotalTokens:      chatResp.Usage.TotalTokens,
				ContextWindow:    contextWindow,
			}
			if callbacks.OnTokenUsage != nil {
				callbacks.OnTokenUsage(usage)
			}
			if sessionID != "" {
				o.db.UpdateSessionTokens(sessionID, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
			}
		}

		if errorState != nil && !validateErrorFix(errorState, pendingCalls) {
			maxRetries := o.config.MaxErrorRetries
			if maxRetries <= 0 {
				maxRetries = 3
			}
			if errorState.Retries > maxRetries {
				log.Printf("[Orchestrator] 错误纠正失败（%d次），放弃", maxRetries)
				errorState = nil
				if len(pendingCalls) == 0 {
					if callbacks.OnTurnComplete != nil {
						callbacks.OnTurnComplete(turnID, sessionID)
					}
					return
				}
			} else {
				errorState.Retries++
				retryMsg := buildRetryMessage(errorState, maxRetries)
				baseMessages = append(baseMessages, llm.ChatMessage{Role: "system", Content: retryMsg})
				continue
			}
		}

		if len(pendingCalls) > 0 {
			batchIteration++
			assistantChatMsg := llm.ChatMessage{
				Role:    "assistant",
				Content: chatResp.Content,
			}
			if chatResp.ReasoningContent != "" {
				assistantChatMsg.ReasoningContent = chatResp.ReasoningContent
			}
			for _, pc := range pendingCalls {
				assistantChatMsg.ToolCalls = append(assistantChatMsg.ToolCalls, llm.ToolCallInfo{
					ID:   pc.ID,
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: pc.Name, Arguments: pc.Args},
				})
			}
			baseMessages = append(baseMessages, assistantChatMsg)

			tcList := make([]map[string]any, 0, len(pendingCalls))
			for _, pc := range pendingCalls {
				tcList = append(tcList, map[string]any{"name": pc.Name, "arguments": pc.Args, "id": pc.ID})
			}
			tcListJSON, _ := json.Marshal(tcList)
			dbAssistantMsg.ToolCall = json.RawMessage(tcListJSON)
			o.db.InsertMessage(dbAssistantMsg)

			for _, pc := range pendingCalls {
				msgs, stop := o.hooks.OnToolResult(ctx, sessionID, turnID, approvalMode, pc, callbacks, &errorState)
				baseMessages = append(baseMessages, msgs...)
				if o.hooks.OnAfterToolExec != nil {
					o.hooks.OnAfterToolExec(pc.Name, nil, "")
				}
				if stop {
					break
				}
			}
			continue
		}

		if o.hooks.OnAfterTurn != nil {
			o.hooks.OnAfterTurn(sessionID, turnID)
		}
		if callbacks.OnTurnComplete != nil {
			callbacks.OnTurnComplete(turnID, sessionID)
		}
		return
	}
}

func (o *Orchestrator) generateTitle(ctx context.Context, userMessage, model, providerID string) string {
	if o.config.TitleGenerator != nil {
		return o.config.TitleGenerator(ctx, userMessage, model, providerID)
	}
	// 默认行为：使用 LLM 生成标题
	if providerID == "" || model == "" {
		return ""
	}
	messages := []llm.ChatMessage{
		{Role: "system", Content: "Generate a concise session title (max 20 chars) based on the user's first message. Return only the title, no quotes or explanation."},
		{Role: "user", Content: userMessage},
	}
	req := &llm.ChatRequest{
		ProviderID:  providerID,
		Model:       model,
		Messages:    messages,
		Temperature: 0.3,
		Thinking:    "off",
	}
	resp, err := o.llmSvc.Chat(ctx, req)
	if err != nil || resp == nil {
		return ""
	}
	title := resp.Content
	if len(title) > 50 {
		title = title[:50] + "..."
	}
	return title
}

func buildRetryMessage(es *AgentErrorState, maxRetries int) string {
	switch es.ErrType {
	case AgentErrCompileFail:
		lastAttemptHint := ""
		if es.LastAttempt != "" {
			lastAttemptHint = fmt.Sprintf("上次尝试的方案（%s）无效，", es.LastAttempt)
		}
		return fmt.Sprintf("%s请使用 read_file/edit_file 等工具修复以上错误（第%d/%d次）。不要解释，直接操作。", lastAttemptHint, es.Retries, maxRetries)
	case AgentErrToolFail:
		return fmt.Sprintf("工具调用失败，请重试或使用不同的工具（第%d/%d次）。", es.Retries, maxRetries)
	default:
		return fmt.Sprintf("必须用工具修复以上错误（第%d/%d次）。不要解释，直接操作。", es.Retries, maxRetries)
	}
}

func validateErrorFix(es *AgentErrorState, pendingCalls []PendingCall) bool {
	return len(pendingCalls) == 0
}
