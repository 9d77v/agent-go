# agent-go

通用 AI Agent 框架 (Go Library)，基于 Google ADK-Go v2。

## 架构

```
agent-go/
├── agent/          # ADK-Go Agent 封装层
│   ├── agent.go         # Agent 外观（NewAgent/SetModel/SetTools/BuildAndRun）
│   ├── adk_runner.go    # ADK Runner 封装（Event→回调转换）
│   ├── chat_model.go    # Chat Completions API 适配器
│   ├── responses_model.go # Responses API 适配器
│   └── types.go         # OrchestratorCallbacks/ImageInput
├── config/         # 通用配置管理
│   ├── manager.go       # ConfigManager（双文件 JSON）
│   └── tool_tree.go     # ToolTreeNode 树操作工具
├── credential/     # 凭据管理（Windows Credential Manager）
├── database/       # 数据库服务
│   ├── service.go       # DBService（ADK session.Service 懒加载）
│   ├── types.go         # Message/Session 通用类型
│   ├── session_ext.go   # 会话扩展存储
│   └── sqlite_util.go   # Checkpoint/Vacuum 工具
├── llm/            # LLM 供应商抽象
│   ├── service.go       # LLMService
│   ├── types.go         # 请求/响应类型
│   └── provider.go      # 请求构建/响应解析
├── mcp/            # MCP 服务器管理 + 工具注入
│   ├── manager.go       # MCP 管理器
│   ├── builtin.go       # 内置 MCP 服务器
│   ├── init.go          # 初始化
│   └── ...
├── skills/         # 技能系统（3级加载 + YAML frontmatter）
├── stream/         # WebSocket 流式通信
│   ├── types.go         # StreamMessage/WsRequest/Orchestrator 接口
│   ├── manager.go       # StreamManager
│   └── ws.go            # WSServer
└── tool/           # 工具执行系统
    ├── types.go         # ToolResult/ToolHandler 类型
    ├── executor.go      # 工具注册表和执行器
    ├── adk_adapter.go   # ADK Tool 适配器
    ├── definitions.go   # 通用工具定义
    └── to_openai.go     # ADK→OpenAI 工具格式转换
```

## 核心概念

### Agent (`agent.NewAgent`)

ADK-Go Agent 外观：`NewAgent(AgentConfig)` 创建，`SetModel`/`SetResponsesModel` 设置模型，`SetTools`/`SetInstruction`/`SetThinking` 配置，`BuildAndRun` 运行。审批基于 ADK 原生 HITL。

### ConfigManager (`config.ConfigManager`)

双文件 JSON 配置管理。

### LLMService (`llm.LLMService`)

多供应商 LLM 路由，支持流式 SSE。

### StreamManager + WSServer (`stream/`)

WebSocket 流式通信。

### 工具系统 (`tool/`)

- `ToolExecutor` — 注册表模式工具执行
- `NewAdkTool()` — 将 handler 包装为 ADK `tool.Tool`
- `DefaultToolDefinitions()` — 通用工具定义
- `ToOpenAIDefinitions()` — ADK 格式 → OpenAI 格式转换

### 数据库 (`database/`)

使用 ADK `session.Service` 管理消息/会话。应用层数据由项目自行管理。

### MCP (`mcp/`)

MCP 服务器管理与工具注入，提供动态工具注册能力。
