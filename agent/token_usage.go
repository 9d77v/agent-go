package agent

import (
	"encoding/json"
	"sync"

	ftool "github.com/9d77v/agent-go/tool"
)

// WrapTokenUsageCallbacks 包装 OrchestratorCallbacks，自动累积 token 用量
// 并在每次用量事件时实时持久化，同时在 turn_complete 时兜底保存最终值。
// saveFn 接收 JSON 序列化后的完整 TokenUsageInfo（含 context_window 及上下文细分字段），
// 仅在 total_tokens > 0 时调用（避免空值覆盖）。
func WrapTokenUsageCallbacks(callbacks *OrchestratorCallbacks, saveFn func(tokenUsageJSON string)) {
	var (
		current ftool.TokenUsageInfo
		tokenMu sync.Mutex
	)

	// 持久化当前累积的 token 用量（total_tokens <= 0 时不写）。
	saveCurrent := func() {
		tokenMu.Lock()
		defer tokenMu.Unlock()
		if current.TotalTokens <= 0 {
			return
		}
		tuJSON, _ := json.Marshal(current)
		saveFn(string(tuJSON))
	}

	origOnTokenUsage := callbacks.OnTokenUsage
	callbacks.OnTokenUsage = func(tu ftool.TokenUsageInfo) {
		tokenMu.Lock()
		current = tu
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
