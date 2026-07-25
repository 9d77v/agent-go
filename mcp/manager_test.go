package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	m := NewManager()
	assert.NotNil(t, m)
	assert.Empty(t, m.GetConfigs())
	assert.Empty(t, m.GetAllServerInfo())
}

func TestSetConfigsAndGet(t *testing.T) {
	m := NewManager()
	cfg := MCPServerConfig{ID: "test", Name: "Test Server", Transport: TransportStdio, Command: "echo"}
	m.SetConfigs([]MCPServerConfig{cfg})
	configs := m.GetConfigs()
	require.Len(t, configs, 1)
	assert.Equal(t, "test", configs[0].ID)
}

func TestAddConfig(t *testing.T) {
	m := NewManager()
	m.AddConfig(MCPServerConfig{ID: "a", Name: "A"})
	m.AddConfig(MCPServerConfig{ID: "b", Name: "B"})
	assert.Len(t, m.GetConfigs(), 2)
}

func TestUpdateConfigNotFound(t *testing.T) {
	m := NewManager()
	err := m.UpdateConfig(context.Background(), "nonexistent", MCPServerConfig{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "未找到")
}

func TestRemoveConfig(t *testing.T) {
	m := NewManager()
	m.AddConfig(MCPServerConfig{ID: "to-remove", Name: "Remove Me"})
	err := m.RemoveConfig("to-remove")
	assert.NoError(t, err)
	assert.Len(t, m.GetConfigs(), 0)
}

func TestRemoveConfigNotFound(t *testing.T) {
	m := NewManager()
	err := m.RemoveConfig("nonexistent")
	assert.Error(t, err)
}

func TestGetServerInfoNotFound(t *testing.T) {
	m := NewManager()
	info := m.GetServerInfo("nonexistent")
	assert.Nil(t, info)
}

func TestGetServerInfoStopped(t *testing.T) {
	m := NewManager()
	m.AddConfig(MCPServerConfig{ID: "stopped-srv", Name: "Stopped"})
	info := m.GetServerInfo("stopped-srv")
	require.NotNil(t, info)
	assert.Equal(t, StatusStopped, info.Status)
}

func TestGetAllServerInfoEmpty(t *testing.T) {
	m := NewManager()
	all := m.GetAllServerInfo()
	assert.Empty(t, all)
}

func TestGetAllServerInfoWithConfigs(t *testing.T) {
	m := NewManager()
	m.AddConfig(MCPServerConfig{ID: "srv1", Name: "Server 1"})
	m.AddConfig(MCPServerConfig{ID: "srv2", Name: "Server 2"})
	all := m.GetAllServerInfo()
	assert.Len(t, all, 2)
}

func TestToggleNotRunning(t *testing.T) {
	m := NewManager()
	m.AddConfig(MCPServerConfig{ID: "toggle-test", Name: "Toggle", Command: "echo", Enabled: false})

	// Toggle enabled - will try to start, expect error since "echo" isn't a real MCP server
	err := m.Toggle(context.Background(), "toggle-test", true)
	assert.Error(t, err) // Will fail because echo isn't an MCP server

	info := m.GetServerInfo("toggle-test")
	require.NotNil(t, info)
	assert.Equal(t, StatusError, info.Status)
}

func TestBuildToolDefinitionsEmpty(t *testing.T) {
	m := NewManager()
	defs := m.BuildToolDefinitions(nil)
	assert.Nil(t, defs)
}

func TestOnServerStartedCallback(t *testing.T) {
	m := NewManager()
	called := false
	m.OnServerStarted = func(cfg MCPServerConfig) {
		called = true
		assert.Equal(t, "cb-test", cfg.ID)
	}

	m.AddConfig(MCPServerConfig{ID: "cb-test", Name: "Callback Test", Command: "nonexistent-cmd-should-fail"})
	// Start will fail (no real server), so OnServerStarted should NOT be called
	err := m.Start(context.Background(), "cb-test")
	assert.Error(t, err)
	assert.False(t, called, "OnServerStarted should not be called on failed start")
}

func TestNewManagerClientInfo(t *testing.T) {
	m := NewManager()
	assert.Equal(t, "agent-go", m.clientName)
	assert.Equal(t, "1.0.0", m.clientVersion)
}

func TestConfigValidationCommand(t *testing.T) {
	m := NewManager()
	m.AddConfig(MCPServerConfig{ID: "empty-cmd", Name: "Empty Cmd", Transport: TransportStdio, Command: ""})
	err := m.Start(context.Background(), "empty-cmd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "命令不能为空")
}

func TestConfigValidationPathTraversal(t *testing.T) {
	m := NewManager()
	m.AddConfig(MCPServerConfig{ID: "traversal", Name: "Traversal", Transport: TransportStdio, Command: "../evil"})
	err := m.Start(context.Background(), "traversal")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "路径遍历")
}

func TestConfigValidationTooManyArgs(t *testing.T) {
	m := NewManager()
	args := make([]string, 101)
	for i := range args {
		args[i] = "a"
	}
	m.AddConfig(MCPServerConfig{ID: "too-many-args", Name: "Too Many Args", Transport: TransportStdio, Command: "echo", Args: args})
	err := m.Start(context.Background(), "too-many-args")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "参数过多")
}

func TestConfigValidationEnvFormat(t *testing.T) {
	m := NewManager()
	m.AddConfig(MCPServerConfig{ID: "bad-env", Name: "Bad Env", Transport: TransportStdio, Command: "echo", Env: []string{"NO_EQUALS"}})
	err := m.Start(context.Background(), "bad-env")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "环境变量格式无效")
}

func TestConfigValidationEnvFormatEmptyKey(t *testing.T) {
	m := NewManager()
	m.AddConfig(MCPServerConfig{ID: "empty-key-env", Name: "Empty Key Env", Transport: TransportStdio, Command: "echo", Env: []string{"=value"}})
	err := m.Start(context.Background(), "empty-key-env")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "环境变量格式无效")
}
