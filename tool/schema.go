package tool

import "google.golang.org/genai"

// schemaTypeToOpenAI genai 类型枚举 → OpenAI 兼容 JSON Schema 类型名（小写）。
// 与 adk_adapter.go 的 typeFromString（OpenAI → genai）互为逆映射，同包维护。
var schemaTypeToOpenAI = map[genai.Type]string{
	genai.TypeUnspecified: "object",
	genai.TypeObject:      "object",
	genai.TypeString:      "string",
	genai.TypeNumber:      "number",
	genai.TypeInteger:     "integer",
	genai.TypeBoolean:     "boolean",
	genai.TypeArray:       "array",
}

// SchemaToOpenAI converts *genai.Schema to a plain map for JSON serialization,
// with lowercase type names expected by OpenAI-compatible APIs.
func SchemaToOpenAI(s *genai.Schema) map[string]any {
	if s == nil {
		return nil
	}
	m := map[string]any{
		"type": schemaTypeToOpenAIType(s.Type),
	}
	if s.Description != "" {
		m["description"] = s.Description
	}
	if len(s.Required) > 0 {
		m["required"] = s.Required
	}
	if len(s.Enum) > 0 {
		m["enum"] = s.Enum
	}
	if len(s.Properties) > 0 {
		props := make(map[string]any, len(s.Properties))
		for k, v := range s.Properties {
			props[k] = SchemaToOpenAI(v)
		}
		m["properties"] = props
	}
	if s.Items != nil {
		m["items"] = SchemaToOpenAI(s.Items)
	}
	return m
}

// schemaTypeToOpenAIType 将 genai 类型枚举映射为 OpenAI 小写类型名；未知类型回退 "string"。
func schemaTypeToOpenAIType(t genai.Type) string {
	if s, ok := schemaTypeToOpenAI[t]; ok {
		return s
	}
	return "string"
}
