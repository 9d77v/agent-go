package tool

import (
	fwllm "github.com/9d77v/agent-go/llm"
)

// ToOpenAIDefinitions converts a slice of ToolDefinition (ADK format)
// to OpenAI-compatible tool definitions for use with non-ADK LLM calls.
func ToOpenAIDefinitions(defs []ToolDefinition) []fwllm.OpenAIToolDefinition {
	result := make([]fwllm.OpenAIToolDefinition, 0, len(defs))
	for _, d := range defs {
		result = append(result, fwllm.OpenAIToolDefinition{
			Type: "function",
			Function: fwllm.OpenAIFunctionDef{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.Parameters,
			},
		})
	}
	return result
}
