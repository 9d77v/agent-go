package mcp

type TransportType string

const (
	TransportStdio TransportType = "stdio"
	TransportSSE   TransportType = "sse"
)

type ServerStatus string

const (
	StatusStopped ServerStatus = "stopped"
	StatusRunning ServerStatus = "running"
	StatusError   ServerStatus = "error"
)

type MCPServerConfig struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Transport TransportType `json:"transport"`
	Command   string        `json:"command,omitempty"`
	Args      []string      `json:"args,omitempty"`
	Env       []string      `json:"env,omitempty"`
	URL       string        `json:"url,omitempty"`
	Enabled   bool          `json:"enabled"`
	BuiltIn   bool          `json:"built_in"`
}

type MCPTool struct {
	ServerID    string `json:"server_id"`
	ServerName  string `json:"server_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type MCPServerInfo struct {
	Config MCPServerConfig `json:"config"`
	Status ServerStatus    `json:"status"`
	Error  string          `json:"error,omitempty"`
	Tools  []MCPTool       `json:"tools"`
}
