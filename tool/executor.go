package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// ToolExecutor 基于注册表的工具执行器
type ToolExecutor struct {
	mu       sync.RWMutex
	handlers map[string]ToolHandler
}

// NewToolExecutor 创建空的工具执行器
func NewToolExecutor() *ToolExecutor {
	return &ToolExecutor{
		handlers: make(map[string]ToolHandler),
	}
}

// Register 注册一个工具处理器
func (e *ToolExecutor) Register(name string, handler ToolHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers[name] = handler
}

// Get 获取已注册的工具处理器
func (e *ToolExecutor) Get(name string) (ToolHandler, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	h, ok := e.handlers[name]
	return h, ok
}

// ListTools 列出所有已注册的工具名
func (e *ToolExecutor) ListTools() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	names := make([]string, 0, len(e.handlers))
	for name := range e.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Execute 统一执行入口
func (e *ToolExecutor) Execute(ctx context.Context, toolName string, args json.RawMessage) *ToolResult {
	e.mu.RLock()
	handler, ok := e.handlers[toolName]
	e.mu.RUnlock()
	if !ok {
		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("未知工具: %s", toolName),
		}
	}
	return handler(ctx, args)
}
