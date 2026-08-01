package agent

import (
	"encoding/json"
	"sync"

	ftool "github.com/9d77v/agent-go/tool"
)

// AccumulatedTokens 累积的 token 用量（只保留最新一次 LLM 调用的值）
type AccumulatedTokens struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// WrapTokenUsageCallbacks 包装 OrchestratorCallbacks，自动累积 token 用量
// 并在每次用量事件时实时持久化，同时在 turn_complete 时兜底保存最终值。
// saveFn 接收 JSON 序列化后的 token 用量字符串（格式: {"prompt_tokens":N,"completion_tokens":N,"total_tokens":N}），
// 仅在 total_tokens > 0 时调用（避免空值覆盖）。
func WrapTokenUsageCallbacks(callbacks *OrchestratorCallbacks, saveFn func(tokenUsageJSON string)) {
	var (
		accumulatedTokens AccumulatedTokens
		tokenMu           sync.Mutex
	)

	// 持久化当前累积的 token 用量（total_tokens <= 0 时不写）。
	saveCurrent := func() {
		tokenMu.Lock()
		defer tokenMu.Unlock()
		if accumulatedTokens.TotalTokens <= 0 {
			return
		}
		tuJSON, _ := json.Marshal(map[string]any{
			"prompt_tokens":     accumulatedTokens.PromptTokens,
			"completion_tokens": accumulatedTokens.CompletionTokens,
			"total_tokens":      accumulatedTokens.TotalTokens,
		})
		saveFn(string(tuJSON))
	}

	origOnTokenUsage := callbacks.OnTokenUsage
	callbacks.OnTokenUsage = func(tu ftool.TokenUsageInfo) {
		tokenMu.Lock()
		accumulatedTokens.PromptTokens = tu.PromptTokens
		accumulatedTokens.CompletionTokens = tu.CompletionTokens
		accumulatedTokens.TotalTokens = tu.TotalTokens
		tokenMu.Unlock()
		// 实时持久化：即使运行中途被打断（max_iterations/取消/错误），token 用量也已写入数据库。
		saveCurrent()
		if origOnTokenUsage != nil {
			origOnTokenUsage(tu)
		}
	}

	origOnTurnComplete := callbacks.OnTurnComplete
	callbacks.OnTurnComplete = func(turnID string, sessionID string) {
		// 回合完成时再次保存最终值（幂等，兜底）。
		saveCurrent()
		if origOnTurnComplete != nil {
			origOnTurnComplete(turnID, sessionID)
		}
	}
}
