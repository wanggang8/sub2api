package cursor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeChatCompletionsRequestBodyMixedInput(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-4.1",
		"instructions": "system prompt",
		"reasoning": {"effort": "medium"},
		"input": [
			{"role": "user", "content": [{"type": "input_text", "text": "hello"}]},
			{"type": "function_call_output", "call_id": "call_1", "output": "done"}
		],
		"tool_choice": "auto"
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Contains(t, payload, "messages")
	require.Equal(t, "auto", payload["tool_choice"])

	messages, ok := payload["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 3)

	systemMsg := messages[0].(map[string]any)
	require.Equal(t, "system", systemMsg["role"])

	userMsg := messages[1].(map[string]any)
	require.Equal(t, "user", userMsg["role"])

	toolMsg := messages[2].(map[string]any)
	require.Equal(t, "tool", toolMsg["role"])
	require.Equal(t, "call_1", toolMsg["tool_call_id"])
	require.Equal(t, "done", toolMsg["content"])
}

func TestNormalizeChatCompletionsRequestBodyPreservesMultipleTools(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-4.1",
		"input": [{"role": "user", "content": [{"type": "input_text", "text": "hello"}]}],
		"tools": [
			{"type": "function", "name": "tool_a", "parameters": {"type": "object"}},
			{"type": "function", "function": {"name": "tool_b", "parameters": {"type": "object"}}}
		],
		"tool_choice": {"type": "function", "name": "tool_b"}
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)
	toolChoice, ok := payload["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", toolChoice["type"])
}

func TestNormalizeMessagesRequestBodyStructuredInputPreserved(t *testing.T) {
	raw := []byte(`{
		"model": "claude-sonnet",
		"tools": [
			{"name": "tool_a", "description": "test", "parameters": {"type": "object", "properties": {"q": {"type": "string"}}}}
		],
		"messages": [
			{"role": "assistant", "content": [{"type": "tool_use", "id": "toolu_1", "name": "tool_a", "input": "{\"q\":\"hello\"}"}]}
		]
	}`)

	normalized, err := NormalizeMessagesRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool := tools[0].(map[string]any)
	require.Contains(t, tool, "input_schema")
	messages, ok := payload["messages"].([]any)
	require.True(t, ok)
	content := messages[0].(map[string]any)["content"].([]any)
	toolUse := content[0].(map[string]any)
	require.Equal(t, `{"q":"hello"}`, toolUse["input"])
}
