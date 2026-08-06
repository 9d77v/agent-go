// Package tool provides ADK-Go tool.Tool adapters for agent-go consumers.
package tool

import (
	"encoding/json"
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	adktool "google.golang.org/adk/v2/tool"
	adktoolutils "google.golang.org/adk/v2/tool/toolutils"
	"google.golang.org/genai"
)

// AdkToolConfig configures an ADK tool adapter.
type AdkToolConfig struct {
	Name        string
	Description string
	Parameters  map[string]any // OpenAI-style parameter definitions
}

// NewAdkTool creates an ADK-compatible tool from a handler and config.
func NewAdkTool(cfg AdkToolConfig, handler ToolHandler) adktool.Tool {
	schema := &genai.Schema{Type: genai.TypeObject}
	if props, ok := cfg.Parameters["properties"]; ok {
		if m, ok := props.(map[string]any); ok {
			schema.Properties = convertProperties(m)
		}
	}
	if required, ok := cfg.Parameters["required"]; ok {
		if arr, ok := required.([]any); ok {
			schema.Required = make([]string, len(arr))
			for i, v := range arr {
				schema.Required[i] = fmt.Sprintf("%v", v)
			}
		}
	}
	return &adkTool{
		name:        cfg.Name,
		description: cfg.Description,
		params:      schema,
		handler:     handler,
	}
}

type adkTool struct {
	name        string
	description string
	params      *genai.Schema
	handler     ToolHandler
}

func (t *adkTool) Name() string        { return t.name }
func (t *adkTool) Description() string { return t.description }
func (t *adkTool) IsLongRunning() bool { return false }

func (t *adkTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.name,
		Description: t.description,
		Parameters:  t.params,
	}
}

func (t *adkTool) Run(ctx agent.Context, args any) (map[string]any, error) {
	raw, _ := json.Marshal(args)
	// 透传 ctx：ADK agent.Context 携带 SessionID()，供工具按调用获取会话身份（支持并行子 Agent）
	result := t.handler(ctx, raw)
	if !result.Success {
		return nil, fmt.Errorf("%s", result.Error)
	}
	return map[string]any{"output": result.Output}, nil
}

// ProcessRequest 实现 toolinternal.RequestProcessor 接口，
// 将工具声明打包到 LLM 请求中，供 ADK flow 预处理调用。
func (t *adkTool) ProcessRequest(ctx agent.Context, req *model.LLMRequest) error {
	return adktoolutils.PackTool(req, t)
}

var _ adktool.Tool = (*adkTool)(nil)

// convertProperties converts map[string]any param definitions to genai.Schema.
func convertProperties(props map[string]any) map[string]*genai.Schema {
	result := make(map[string]*genai.Schema, len(props))
	for name, def := range props {
		if defMap, ok := def.(map[string]any); ok {
			s := &genai.Schema{}
			if t, ok := defMap["type"].(string); ok {
				s.Type = genai.Type(t)
			}
			if desc, ok := defMap["description"].(string); ok {
				s.Description = desc
			}
			if enum, ok := defMap["enum"]; ok {
				if arr, ok := enum.([]any); ok {
					s.Enum = make([]string, len(arr))
					for i, v := range arr {
						s.Enum[i] = fmt.Sprintf("%v", v)
					}
				}
			}
			if nested, ok := defMap["properties"]; ok {
				if nestedMap, ok := nested.(map[string]any); ok {
					s.Properties = convertProperties(nestedMap)
				}
			}
			if items, ok := defMap["items"]; ok {
				s.Items = convertItems(items)
			}
			result[name] = s
		}
	}
	return result
}

func convertItems(items any) *genai.Schema {
	if m, ok := items.(map[string]any); ok {
		s := &genai.Schema{}
		if t, ok := m["type"].(string); ok {
			s.Type = genai.Type(t)
		}
		if enum, ok := m["enum"]; ok {
			if arr, ok := enum.([]any); ok {
				s.Enum = make([]string, len(arr))
				for i, v := range arr {
					s.Enum[i] = fmt.Sprintf("%v", v)
				}
			}
		}
		return s
	}
	return nil
}
