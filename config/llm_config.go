// Package config 提供 LLM 供应商配置相关类型。
package config

// LLMProviderConfig LLM 供应商配置。
type LLMProviderConfig struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	ProviderType string   `json:"provider_type"` // openai, anthropic, gemini
	BaseURL      string   `json:"base_url"`
	APIKey       string   `json:"-"` // 不序列化，由 credential 管理
	Models       []string `json:"models"`
	Enabled      bool     `json:"enabled"`
	WorkspaceID  string   `json:"workspace_id,omitempty"`
}

// ProviderType 常量。
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderGemini    = "gemini"
)
