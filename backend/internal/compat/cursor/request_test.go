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

func TestNormalizeChatCompletionsRequestBodyFunctionOutputStringPreserved(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"input": [
			{"type": "function_call_output", "call_id": "call_1", "output": "plain result"}
		]
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	messages := payload["messages"].([]any)
	require.Len(t, messages, 1)
	toolMsg := messages[0].(map[string]any)
	require.Equal(t, "tool", toolMsg["role"])
	require.Equal(t, "call_1", toolMsg["tool_call_id"])
	require.Equal(t, "plain result", toolMsg["content"])
}

func TestNormalizeChatCompletionsRequestBodyFunctionOutputArrayText(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"input": [
			{
				"type": "function_call_output",
				"call_id": "call_1",
				"output": [
					{"type": "text", "text": "line one"},
					{"type": "output_text", "text": "line two"}
				]
			}
		]
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	messages := payload["messages"].([]any)
	require.Len(t, messages, 1)
	toolMsg := messages[0].(map[string]any)
	require.Equal(t, "tool", toolMsg["role"])
	require.Equal(t, "call_1", toolMsg["tool_call_id"])
	require.Equal(t, "line one\nline two", toolMsg["content"])
}

func TestNormalizeResponsesRequestBodyFunctionOutputArrayText(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"input": [
			{
				"type": "function_call_output",
				"call_id": "call_1",
				"output": [
					{"type": "text", "text": "line one"},
					{"type": "output_text", "text": "line two"}
				]
			}
		]
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	items := payload["input"].([]any)
	require.Len(t, items, 1)
	item := items[0].(map[string]any)
	require.Equal(t, "function_call_output", item["type"])
	require.Equal(t, "call_1", item["call_id"])
	require.Equal(t, "line one\nline two", item["output"])
}

func TestNormalizeResponsesRequestBodyReplacesApplyPatchForInputArray(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"input": [{"role": "user", "content": "update files"}],
		"tools": [
			{"type": "function", "function": {"name": "Read", "parameters": {"type": "object"}}},
			{"type": "function", "function": {"name": "ApplyPatch", "parameters": {"type": "object"}}},
			{"type": "custom", "name": "apply_patch", "custom": {"description": "Patch files", "input_schema": {"type": "object"}}}
		]
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	tools := payload["tools"].([]any)

	names := make(map[string]bool)
	for _, rawTool := range tools {
		tool := rawTool.(map[string]any)
		if fn, ok := tool["function"].(map[string]any); ok {
			names[fn["name"].(string)] = true
			continue
		}
		if name, ok := tool["name"].(string); ok {
			names[name] = true
		}
	}

	require.True(t, names["Read"])
	require.True(t, names["Write"])
	require.True(t, names["StrReplace"])
	require.False(t, names["ApplyPatch"])
	require.False(t, names["apply_patch"])
}

func TestNormalizeResponsesRequestBodyDoesNotRewriteToolsForTextInput(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"input": "update files",
		"tools": [
			{"type": "function", "function": {"name": "ApplyPatch", "parameters": {"type": "object"}}}
		]
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(normalized))
}

func TestNormalizeChatCompletionsRequestBodyAddsCursorEditingToolsForInputArray(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"input": [{"role": "user", "content": "update files"}],
		"tools": [
			{"type": "function", "function": {"name": "Read", "parameters": {"type": "object"}}},
			{"type": "function", "function": {"name": "ApplyPatch", "parameters": {"type": "object"}}}
		]
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	tools := payload["tools"].([]any)

	names := make(map[string]bool)
	for _, rawTool := range tools {
		tool := rawTool.(map[string]any)
		fn := tool["function"].(map[string]any)
		names[fn["name"].(string)] = true
	}

	require.True(t, names["Read"])
	require.True(t, names["Write"])
	require.True(t, names["StrReplace"])
	require.False(t, names["ApplyPatch"])
}

func TestNormalizeChatCompletionsRequestBodyPreservesMultipleTools(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-4.1",
		"input": [{"role": "user", "content": [{"type": "input_text", "text": "hello"}]}],
		"tools": [
			{"type": "function", "name": "tool_a", "parameters": {"type": "object"}},
			{"type": "function", "function": {"name": "tool_b", "parameters": {"type": "object"}}},
			{"name": "tool_c", "description": "Claude style", "input_schema": {"type": "object", "properties": {"path": {"type": "string"}}}},
			{"type": "web_search"}
		],
		"tool_choice": {"type": "any"}
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 6)
	toolA := tools[0].(map[string]any)
	toolAFn := toolA["function"].(map[string]any)
	require.Equal(t, "tool_a", toolAFn["name"])
	require.Contains(t, toolAFn, "parameters")
	toolB := tools[1].(map[string]any)
	toolBFn := toolB["function"].(map[string]any)
	require.Equal(t, "tool_b", toolBFn["name"])
	toolC := tools[2].(map[string]any)
	toolCFn := toolC["function"].(map[string]any)
	require.Equal(t, "tool_c", toolCFn["name"])
	require.Equal(t, "Claude style", toolCFn["description"])
	require.Contains(t, toolCFn, "parameters")
	toolD := tools[3].(map[string]any)
	require.Equal(t, "web_search", toolD["type"])
	toolE := tools[4].(map[string]any)
	toolEFn := toolE["function"].(map[string]any)
	require.Equal(t, "Write", toolEFn["name"])
	toolF := tools[5].(map[string]any)
	toolFFn := toolF["function"].(map[string]any)
	require.Equal(t, "StrReplace", toolFFn["name"])
	require.Equal(t, "required", payload["tool_choice"])
}

func TestNormalizeChatCompletionsRequestBodyRewritesMissingParallelToolInstruction(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "Use ` + "`multi_tool_use.parallel`" + ` to parallelize tool calls and only this."},
			{"role": "user", "content": "hi"}
		],
		"tools": [
			{"type": "function", "function": {"name": "ReadFile", "parameters": {"type": "object"}}},
			{"type": "function", "function": {"name": "rg", "parameters": {"type": "object"}}}
		],
		"tool_choice": "auto"
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	messages := payload["messages"].([]any)
	system := messages[0].(map[string]any)
	content := system["content"].(string)
	require.NotContains(t, content, "multi_tool_use.parallel")
	require.Contains(t, content, "multiple tool_calls in the same assistant response")
	require.Len(t, payload["tools"].([]any), 2)
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
