package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

var GlobalMCPManager *Manager

type Manager struct {
	mu              sync.RWMutex
	configs         []MCPServerConfig
	servers         map[string]*serverInst
	clientName      string
	clientVersion   string
	HTTPProxy       *HTTPProxy
	OnServerStarted func(cfg MCPServerConfig)
}

type serverInst struct {
	config MCPServerConfig
	client *client.Client
	status ServerStatus
	tools  []MCPTool
	err    string
	cancel context.CancelFunc
}

func NewManager() *Manager {
	m := &Manager{
		servers:       make(map[string]*serverInst),
		clientName:    "agent-go",
		clientVersion: "1.0.0",
	}
	m.HTTPProxy = NewHTTPProxy()
	if err := m.HTTPProxy.Start(); err != nil {
		log.Printf("[MCP] HTTP 代理启动失败: %v", err)
		m.HTTPProxy = nil
	}
	return m
}

func (m *Manager) SetConfigs(configs []MCPServerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs = configs
}

func (m *Manager) GetConfigs() []MCPServerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]MCPServerConfig, len(m.configs))
	copy(result, m.configs)
	return result
}

func (m *Manager) AddConfig(cfg MCPServerConfig) {
	m.mu.Lock()
	m.configs = append(m.configs, cfg)
	m.mu.Unlock()
}

func (m *Manager) UpdateConfig(ctx context.Context, id string, cfg MCPServerConfig) error {
	m.mu.Lock()
	idx := -1
	for i := range m.configs {
		if m.configs[i].ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		m.mu.Unlock()
		return fmt.Errorf("未找到 MCP 服务器配置: %s", id)
	}
	m.configs[idx] = cfg
	wasRunning := false
	if inst, ok := m.servers[id]; ok && inst.status == StatusRunning {
		wasRunning = true
	}
	m.mu.Unlock()
	if wasRunning {
		m.Stop(id)
	}
	if cfg.Enabled {
		return m.Start(ctx, id)
	}
	return nil
}

func (m *Manager) RemoveConfig(id string) error {
	m.mu.Lock()
	idx := -1
	for i := range m.configs {
		if m.configs[i].ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		m.mu.Unlock()
		return fmt.Errorf("未找到 MCP 服务器配置: %s", id)
	}
	m.mu.Unlock()
	m.Stop(id)
	m.mu.Lock()
	m.configs = append(m.configs[:idx], m.configs[idx+1:]...)
	m.mu.Unlock()
	return nil
}

func (m *Manager) GetServerInfo(id string) *MCPServerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.servers[id]
	if !ok {
		for _, cfg := range m.configs {
			if cfg.ID == id {
				return &MCPServerInfo{Config: cfg, Status: StatusStopped}
			}
		}
		return nil
	}
	return &MCPServerInfo{Config: inst.config, Status: inst.status, Error: inst.err, Tools: inst.tools}
}

func (m *Manager) GetAllServerInfo() []MCPServerInfo {
	m.mu.RLock()
	configs := make([]MCPServerConfig, len(m.configs))
	copy(configs, m.configs)
	m.mu.RUnlock()
	result := make([]MCPServerInfo, 0, len(configs))
	for _, cfg := range configs {
		if info := m.GetServerInfo(cfg.ID); info != nil {
			result = append(result, *info)
		}
	}
	return result
}

func (m *Manager) StartAll(ctx context.Context) {
	m.mu.RLock()
	configs := make([]MCPServerConfig, len(m.configs))
	copy(configs, m.configs)
	m.mu.RUnlock()
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		if err := m.Start(ctx, cfg.ID); err != nil {
			log.Printf("[MCP] 启动服务器 %s 失败: %v", cfg.Name, err)
		}
	}
}

func (m *Manager) Start(ctx context.Context, id string) error {
	m.mu.Lock()
	var cfg *MCPServerConfig
	for i := range m.configs {
		if m.configs[i].ID == id {
			cfg = &m.configs[i]
			break
		}
	}
	if cfg == nil {
		m.mu.Unlock()
		return fmt.Errorf("未找到 MCP 服务器配置: %s", id)
	}
	if inst, ok := m.servers[id]; ok && inst.status == StatusRunning {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	return m.startServer(ctx, cfg)
}

func (m *Manager) startServer(ctx context.Context, cfg *MCPServerConfig) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("startServer panic: %v", r)
		}
	}()
	// 验证配置
	if cfg.Transport == TransportStdio {
		if cfg.Command == "" {
			return fmt.Errorf("MCP 服务器命令不能为空")
		}
		if strings.Contains(cfg.Command, "..") {
			return fmt.Errorf("MCP 服务器命令不能包含路径遍历")
		}
		if len(cfg.Args) > 100 {
			return fmt.Errorf("MCP 服务器参数过多（最多 100 个）")
		}
		for _, env := range cfg.Env {
			if !strings.Contains(env, "=") || strings.HasPrefix(env, "=") {
				return fmt.Errorf("MCP 环境变量格式无效（需要 KEY=VALUE）: %s", env)
			}
		}
	}
	cctx, cancel := context.WithCancel(ctx)
	var cl *client.Client
	var tools []MCPTool
	var startErr error
	switch cfg.Transport {
	case TransportStdio:
		cl, tools, startErr = m.connectStdio(cctx, cfg)
	default:
		startErr = fmt.Errorf("不支持的传输类型: %s", cfg.Transport)
	}
	if startErr != nil {
		cancel()
		m.mu.Lock()
		m.servers[cfg.ID] = &serverInst{config: *cfg, status: StatusError, err: startErr.Error(), cancel: cancel}
		m.mu.Unlock()
		return startErr
	}
	m.mu.Lock()
	m.servers[cfg.ID] = &serverInst{config: *cfg, client: cl, status: StatusRunning, tools: tools, cancel: cancel}
	m.mu.Unlock()
	// 如果配置了 HTTP 服务地址，自动注册到统一代理
	if cfg.URL != "" && m.HTTPProxy != nil {
		m.HTTPProxy.Register(cfg.ID, cfg.URL)
	}
	log.Printf("[MCP] 服务器 %s 已启动，发现 %d 个工具", cfg.Name, len(tools))
	if m.OnServerStarted != nil {
		m.OnServerStarted(*cfg)
	}
	return nil
}

func (m *Manager) connectStdio(ctx context.Context, cfg *MCPServerConfig) (*client.Client, []MCPTool, error) {
	stdioOpts := []transport.StdioOption{
		transport.WithCommandFunc(func(ctx context.Context, command string, env []string, args []string) (*exec.Cmd, error) {
			return newCommand(command, args, env), nil
		}),
	}
	trans := transport.NewStdioWithOptions(cfg.Command, cfg.Env, cfg.Args, stdioOpts...)
	if err := trans.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("启动子进程失败: %w", err)
	}
	cl := client.NewClient(trans)
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = "2024-11-05"
	initReq.Params.ClientInfo = mcp.Implementation{Name: m.clientName, Version: m.clientVersion}
	if _, err := cl.Initialize(ctx, initReq); err != nil {
		return nil, nil, fmt.Errorf("初始化失败: %w", err)
	}
	toolsResult, err := cl.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, nil, fmt.Errorf("获取工具列表失败: %w", err)
	}
	mcpTools := make([]MCPTool, 0, len(toolsResult.Tools))
	for _, t := range toolsResult.Tools {
		mcpTools = append(mcpTools, MCPTool{ServerID: cfg.ID, ServerName: cfg.Name, Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return cl, mcpTools, nil
}

func (m *Manager) Stop(id string) {
	m.mu.Lock()
	inst, ok := m.servers[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.servers, id)
	m.mu.Unlock()
	if inst.cancel != nil {
		inst.cancel()
	}
}

func (m *Manager) StopAll() {
	m.mu.RLock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		m.Stop(id)
	}
}

func (m *Manager) Toggle(ctx context.Context, id string, enabled bool) error {
	m.mu.Lock()
	for i := range m.configs {
		if m.configs[i].ID == id {
			m.configs[i].Enabled = enabled
			break
		}
	}
	m.mu.Unlock()
	if enabled {
		return m.Start(ctx, id)
	}
	m.Stop(id)
	return nil
}

func (m *Manager) GetAllTools() []MCPTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []MCPTool
	for _, inst := range m.servers {
		if inst.status != StatusRunning {
			continue
		}
		result = append(result, inst.tools...)
	}
	return result
}

func (m *Manager) CallTool(ctx context.Context, serverID string, toolName string, args map[string]any) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("CallTool panic: %v", r)
		}
	}()
	m.mu.RLock()
	inst, ok := m.servers[serverID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("MCP 服务器 %s 未运行", serverID)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args
	// 如果父 context 已有截止时间则直接使用，否则加 30 秒默认超时
	var cctx context.Context
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		cctx = ctx
		cancel = func() {}
	} else {
		cctx, cancel = context.WithTimeout(ctx, 30*time.Second)
	}
	defer cancel()
	result, err = inst.client.CallTool(cctx, req)
	if err != nil {
		return nil, fmt.Errorf("调用工具 %s/%s 失败: %w", serverID, toolName, err)
	}
	return result, nil
}

func (m *Manager) BuildToolDefinitions(toolEnabled func(name string) bool) []map[string]any {
	tools := m.GetAllTools()
	if len(tools) == 0 {
		return nil
	}
	result := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fullName := "mcp_" + strings.ToLower(t.ServerID) + "_" + t.Name
		if toolEnabled != nil && !toolEnabled(fullName) {
			continue
		}
		schemaBytes, _ := json.Marshal(t.InputSchema)
		var schemaMap map[string]any
		json.Unmarshal(schemaBytes, &schemaMap)
		result = append(result, map[string]any{"type": "function", "function": map[string]any{"name": fullName, "description": fmt.Sprintf("[%s] %s", t.ServerName, t.Description), "parameters": schemaMap}})
	}
	return result
}

func (m *Manager) ExecuteTool(toolName string, args json.RawMessage) (ok bool, out string, errStr string) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
			errStr = fmt.Sprintf("MCP ExecuteTool panic: %v", r)
		}
	}()
	if !strings.HasPrefix(toolName, "mcp_") {
		return false, "", ""
	}
	rest := toolName[4:]
	m.mu.RLock()
	configs := make([]MCPServerConfig, len(m.configs))
	copy(configs, m.configs)
	m.mu.RUnlock()
	var serverID, rawToolName string
	for _, cfg := range configs {
		prefix := strings.ToLower(cfg.ID) + "_"
		if strings.HasPrefix(rest, prefix) {
			serverID = cfg.ID
			rawToolName = rest[len(prefix):]
			break
		}
	}
	if serverID == "" {
		return false, "", fmt.Sprintf("未找到服务器: %s", toolName)
	}
	var params map[string]any
	if err := json.Unmarshal(args, &params); err != nil {
		return false, "", fmt.Sprintf("参数解析失败: %v", err)
	}
	cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := m.CallTool(cctx, serverID, rawToolName, params)
	if err != nil {
		return false, "", err.Error()
	}
	data, _ := json.Marshal(result)
	return true, string(data), ""
}

func newCommand(command string, args []string, env []string) *exec.Cmd {
	cmd := exec.Command(command, args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}
