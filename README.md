# agent-go

通用 AI Agent 框架，提供 Agent 编排、MCP 服务器管理、工具执行、消息持久化等基础设施。

## 架构

```
agent-go/
├── agent/          # Agent 编排层
│   ├── orchestrator.go   # 核心编排器（多轮对话循环）
│   ├── message_manager.go # 消息创建/持久化/历史重建
│   ├── change_tracker.go  # 文件变更追踪/撤销
│   ├── interfaces.go      # 接口定义（LLM、Tool、SystemPrompt）
│   └── types.go           # 类型定义
├── llm/            # LLM 供应商抽象
│   ├── types.go           # 请求/响应类型
│   └── provider.go        # OpenAI/Anthropic/Gemini 适配器
├── mcp/            # MCP (Model Context Protocol) 管理
│   ├── manager.go         # MCP 服务器生命周期管理
│   ├── proxy.go           # 统一 HTTP 代理（MCP 服务反向代理）
│   └── types.go           # 配置/工具类型定义
├── msgdb/          # 消息数据库
│   ├── store.go           # SQLite 存储实现
│   └── types.go           # 消息/会话结构体
├── skills/         # 技能系统
│   ├── manager.go         # 技能加载管理器
│   ├── loader.go          # 全局技能注册表 + YAML 解析
│   ├── db_store.go        # 自定义技能数据库 CRUD
│   └── types.go           # 技能元数据结构
└── tool/           # 工具执行系统
    ├── executor.go        # 工具注册表和执行器
    └── types.go           # 工具参数/结果类型
```

## 核心概念

### MCP 管理器 (`mcp.Manager`)

管理 MCP 服务器的完整生命周期：

```go
mgr := mcp.NewManager()
mgr.SetConfigs(configs)
mgr.StartAll(ctx)
```

- 支持 Stdio/SSE 传输协议
- 内置配置验证（命令白名单、参数限制等）
- `OnServerStarted` 回调供应用层注入业务行为
- 统一 HTTP Proxy（`mcp.HTTPProxy`）为 MCP 服务的 HTTP 接口提供反向代理

### Agent 编排器 (`agent.Orchestrator`)

通用多轮对话编排引擎：

```go
orch := agent.NewOrchestrator(llmSvc, toolExec, promptBuilder, toolDefProvider, db, config, hooks)
orch.Orchestrate(ctx, sessionID, message, model, providerID, mode, thinking, approvalMode, callbacks)
```

- 支持流式/非流式 LLM 调用
- 工具调用循环 + 错误重试
- 审批模式（自动/默认/手动）
- 会话标题自动生成（可定制）
- Token 用量追踪

### 工具执行器 (`tool.ToolExecutor`)

```go
executor := tool.NewToolExecutor()
executor.Register("read_file", readFileHandler)
result := executor.Execute("read_file", args)
```

### 消息数据库 (`msgdb.Store`)

基于 SQLite 的会话/消息持久化：

```go
store := msgdb.NewStore(dbPath)
store.Open()
store.InsertMessage(msg)
msgs, _ := store.ListMessages(sessionID)
```

### 技能系统 (`skills.Manager`)

三级技能来源：内置（Builtin）、工作区（Workspace）、自定义（Custom）

## 安全

- MCP 服务器配置验证：Command 非空、禁止路径遍历、Args ≤ 100、Env 格式校验
- LLM 错误消息脱敏
- 可取消的 Context 传播
- panic recovery 覆盖关键路径

## 配置

### MCP 服务器配置

```go
type MCPServerConfig struct {
    ID        string        // 唯一标识
    Name      string        // 显示名称
    Transport TransportType // stdio | sse
    Command   string        // 可执行文件路径
    Args      []string      // 启动参数
    Env       []string      // 环境变量（KEY=VALUE 格式）
    URL       string        // HTTP 服务地址（自动注册到 Proxy）
    Enabled   bool          // 是否启用
    BuiltIn   bool          // 是否内置
}
```

### 编排器配置

```go
type OrchestratorConfig struct {
    IterationsPerBatch int           // 每批次最大工具调用轮数
    ContextWindow      int           // 上下文窗口大小
    OutputReserve      int           // 输出保留 token 数
    WorkspaceID        string        // 工作区标识
    MaxErrorRetries    int           // 最大错误重试次数
    TitleGenerator     func(...)     // 自定义标题生成函数
}
```

## 测试

```bash
go test ./... -v -count=1
```

项目包含 50+ 个测试用例，覆盖 mcp、msgdb、tool、agent、skills 五个包。

## 使用示例

### 作为库引用

```go
import (
    "github.com/9d77v/agent-go/mcp"
    "github.com/9d77v/agent-go/agent"
    ftool "github.com/9d77v/agent-go/tool"
)
```

```go
// 创建 MCP 管理器
mgr := mcp.NewManager()
mgr.SetConfigs([]mcp.MCPServerConfig{{
    ID: "my-server", Name: "My MCP Server",
    Transport: mcp.TransportStdio,
    Command:   "/path/to/server",
    Enabled:   true,
}})
mgr.StartAll(context.Background())

// 获取所有注册的工具
tools := mgr.GetAllTools()

// 统一 HTTP 代理
proxyURL := mgr.HTTPProxy.ProxyURL("my-server")
```
