package cursor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func requirePayloadSlice(t *testing.T, payload map[string]any, key string) []any {
	t.Helper()
	items, ok := payload[key].([]any)
	require.Truef(t, ok, "expected %q to be []any", key)
	return items
}

func requireMapValue(t *testing.T, raw any) map[string]any {
	t.Helper()
	item, ok := raw.(map[string]any)
	require.True(t, ok)
	return item
}

func requireMapField(t *testing.T, item map[string]any, key string) map[string]any {
	t.Helper()
	field, ok := item[key].(map[string]any)
	require.Truef(t, ok, "expected %q to be map[string]any", key)
	return field
}

func requireSliceField(t *testing.T, item map[string]any, key string) []any {
	t.Helper()
	field, ok := item[key].([]any)
	require.Truef(t, ok, "expected %q to be []any", key)
	return field
}

func requireStringField(t *testing.T, item map[string]any, key string) string {
	t.Helper()
	field, ok := item[key].(string)
	require.Truef(t, ok, "expected %q to be string", key)
	return field
}

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

	messages := requirePayloadSlice(t, payload, "messages")
	require.Len(t, messages, 3)

	systemMsg := requireMapValue(t, messages[0])
	require.Equal(t, "system", systemMsg["role"])

	userMsg := requireMapValue(t, messages[1])
	require.Equal(t, "user", userMsg["role"])

	toolMsg := requireMapValue(t, messages[2])
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
	messages := requirePayloadSlice(t, payload, "messages")
	require.Len(t, messages, 1)
	toolMsg := requireMapValue(t, messages[0])
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
	messages := requirePayloadSlice(t, payload, "messages")
	require.Len(t, messages, 1)
	toolMsg := requireMapValue(t, messages[0])
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
	items := requirePayloadSlice(t, payload, "input")
	require.Len(t, items, 1)
	item := requireMapValue(t, items[0])
	require.Equal(t, "function_call_output", item["type"])
	require.Equal(t, "call_1", item["call_id"])
	require.Equal(t, "line one\nline two", item["output"])
}

func TestNormalizeResponsesRequestBodyConvertsCustomToolHistoryToFunctionHistory(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"input": [
			{"type": "custom_tool_call", "call_id": "call_1", "name": "ApplyPatch", "input": "*** Begin Patch\n*** End Patch"},
			{"type": "custom_tool_call_output", "call_id": "call_1", "output": [{"type": "input_text", "text": "ok"}]}
		]
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	items := requirePayloadSlice(t, payload, "input")
	require.Len(t, items, 2)

	call := requireMapValue(t, items[0])
	require.Equal(t, "function_call", call["type"])
	require.Equal(t, "call_1", call["call_id"])
	require.Equal(t, "ApplyPatch", call["name"])
	require.Equal(t, "*** Begin Patch\n*** End Patch", call["arguments"])
	require.NotContains(t, call, "input")

	output := requireMapValue(t, items[1])
	require.Equal(t, "function_call_output", output["type"])
	require.Equal(t, "call_1", output["call_id"])
	require.Equal(t, "ok", output["output"])
}

func TestNormalizeOpenAIChatCompletionsRequestBodyWrapsApplyPatchHistory(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.5",
		"input": [
			{"type": "custom_tool_call", "call_id": "call_1", "name": "ApplyPatch", "input": "*** Begin Patch\n*** Update File: /tmp/a.txt\n@@\n-old line\n*** End Patch"},
			{"type": "custom_tool_call_output", "call_id": "call_1", "output": [{"type": "input_text", "text": "ok"}]}
		],
		"tools": [
			{"type":"custom","name":"ApplyPatch","description":"Patch files"}
		]
	}`)

	normalized, err := NormalizeOpenAIChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	items := requirePayloadSlice(t, payload, "input")
	call := requireMapValue(t, items[0])
	require.Equal(t, "function_call", call["type"])
	require.Equal(t, "ApplyPatch", call["name"])
	require.JSONEq(t, `{"patch":"*** Begin Patch\n*** Update File: /tmp/a.txt\n@@\n-old line\n*** End Patch"}`, requireStringField(t, call, "arguments"))
}

func TestNormalizeResponsesRequestBodyPreservesToolsForInputArray(t *testing.T) {
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
	tools := requirePayloadSlice(t, payload, "tools")

	names := make(map[string]bool)
	for _, rawTool := range tools {
		tool := requireMapValue(t, rawTool)
		if fn, ok := tool["function"].(map[string]any); ok {
			names[requireStringField(t, fn, "name")] = true
			continue
		}
		if name, ok := tool["name"].(string); ok {
			names[name] = true
		}
	}

	require.True(t, names["Read"])
	require.True(t, names["ApplyPatch"])
	require.True(t, names["apply_patch"])
	require.False(t, names["Write"])
	require.False(t, names["StrReplace"])
}

func TestNormalizeResponsesRequestBodyConvertsTopLevelChatToolForChatShape(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "search docs"}],
		"tools": [
			{"type": "function", "name": "search_docs", "description": "Search docs", "input_schema": {"type": "object", "properties": {"query": {"type": "string"}}}}
		]
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	tools := requirePayloadSlice(t, payload, "tools")
	require.Len(t, tools, 1)
	tool := requireMapValue(t, tools[0])
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "search_docs", tool["name"])
	require.Equal(t, "Search docs", tool["description"])
	params := requireMapField(t, tool, "parameters")
	require.Equal(t, "object", params["type"])
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

func TestNormalizeChatCompletionsRequestBodyPreservesCursorEditingToolsForInputArray(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"input": [{"role": "user", "content": "update files"}],
		"tools": [
			{"type": "function", "function": {"name": "Read", "parameters": {"type": "object"}}},
			{"type": "function", "function": {"name": "ApplyPatch", "parameters": {"type": "object"}}},
			{"type": "custom", "name": "apply_patch", "description": "Patch files", "input_schema": {"type": "object"}}
		]
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	tools := requirePayloadSlice(t, payload, "tools")

	names := make(map[string]bool)
	for _, rawTool := range tools {
		tool := requireMapValue(t, rawTool)
		fn := requireMapField(t, tool, "function")
		names[requireStringField(t, fn, "name")] = true
	}

	require.True(t, names["Read"])
	require.True(t, names["ApplyPatch"])
	require.True(t, names["apply_patch"])
	require.False(t, names["Write"])
	require.False(t, names["StrReplace"])
}

func TestNormalizeOpenAIChatCompletionsRequestBodyBridgesApplyPatchCustomTool(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.5",
		"user": "cursor-user-hash",
		"input": [{"role": "user", "content": "update files"}],
		"tools": [
			{
				"type": "custom",
				"name": "ApplyPatch",
				"description": "Patch files",
				"format": {
					"type": "grammar",
					"syntax": "lark",
					"definition": "start: begin_patch hunk end_patch"
				}
			}
		]
	}`)

	normalized, err := NormalizeOpenAIChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	_, hasMessages := payload["messages"]
	require.False(t, hasMessages)
	_, hasUser := payload["user"]
	require.False(t, hasUser)
	input := requirePayloadSlice(t, payload, "input")
	require.Len(t, input, 1)
	tools := requirePayloadSlice(t, payload, "tools")
	require.Len(t, tools, 1)
	tool := requireMapValue(t, tools[0])
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "ApplyPatch", tool["name"])
	require.NotContains(t, tool, "format")
	require.Equal(t, "Patch files", tool["description"])
	parameters := requireMapField(t, tool, "parameters")
	require.Equal(t, "object", parameters["type"])
	properties := requireMapField(t, parameters, "properties")
	patch := requireMapField(t, properties, "patch")
	require.Equal(t, "string", patch["type"])
	patchDescription := requireStringField(t, patch, "description")
	require.Contains(t, patchDescription, "*** Begin Patch")
	require.Contains(t, patchDescription, "*** Add File:")
	require.Contains(t, patchDescription, "*** Update File:")
	require.Contains(t, patchDescription, "*** Delete File:")
	require.Contains(t, patchDescription, "+new line")
	require.Contains(t, patchDescription, "-old line")
	require.Equal(t, []any{"patch"}, parameters["required"])
}

func TestNormalizeChatCompletionsRequestBodyDoesNotApplyOpenAIPassthroughSanitizer(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.5",
		"user": "cursor-user-hash",
		"input": [{"role": "user", "content": "update files"}]
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Contains(t, payload, "messages")
	require.Equal(t, "cursor-user-hash", payload["user"])
}

func TestNormalizeChatCompletionsRequestBodyNormalizesTopLevelSystemAndAnthropicToolBlocks(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"system": [{"type":"text","text":"system prompt"}],
		"messages": [
			{"role":"assistant","content":[
				{"type":"text","text":"Working on it"},
				{"type":"tool_use","id":"call_1","name":"ApplyPatch","input":{"input":"*** Begin Patch"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"ok"}]}
			]}
		],
		"tools": [{"name":"ApplyPatch","description":"Patch files","input_schema":{"type":"object"}}],
		"tool_choice": {"type":"any"},
		"previous_response_id":"resp_123"
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.NotContains(t, payload, "system")
	require.NotContains(t, payload, "previous_response_id")
	require.Equal(t, "required", payload["tool_choice"])

	messages := requirePayloadSlice(t, payload, "messages")
	require.Len(t, messages, 3)

	systemMsg := requireMapValue(t, messages[0])
	require.Equal(t, "system", systemMsg["role"])
	require.Equal(t, "system prompt", systemMsg["content"])

	assistantMsg := requireMapValue(t, messages[1])
	require.Equal(t, "assistant", assistantMsg["role"])
	require.Equal(t, "Working on it", assistantMsg["content"])
	toolCalls := requireSliceField(t, assistantMsg, "tool_calls")
	require.Len(t, toolCalls, 1)
	require.Equal(t, "ApplyPatch", requireMapField(t, requireMapValue(t, toolCalls[0]), "function")["name"])

	toolMsg := requireMapValue(t, messages[2])
	require.Equal(t, "tool", toolMsg["role"])
	require.Equal(t, "call_1", toolMsg["tool_call_id"])
	require.Equal(t, "ok", toolMsg["content"])

	tools := requirePayloadSlice(t, payload, "tools")
	require.Len(t, tools, 1)
	function := requireMapField(t, requireMapValue(t, tools[0]), "function")
	require.Equal(t, "ApplyPatch", function["name"])
	require.Equal(t, "Patch files", function["description"])
}

func TestNormalizeChatCompletionsRequestBodyRepairsDirtyAnthropicToolArguments(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role":"assistant","content":[
				{"type":"tool_use","id":"call_1","name":"str_replace_editor","input":{
					"file_path":"src/main.go",
					"old_string":"fmt.Println(“hello”)",
					"new_string":"fmt.Println(‘hi’)"
				}}
			]}
		]
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	messages := requirePayloadSlice(t, payload, "messages")
	require.Len(t, messages, 1)
	assistantMsg := requireMapValue(t, messages[0])
	toolCalls := requireSliceField(t, assistantMsg, "tool_calls")
	require.Len(t, toolCalls, 1)
	function := requireMapField(t, requireMapValue(t, toolCalls[0]), "function")
	require.Equal(t, "str_replace_editor", function["name"])

	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(requireStringField(t, function, "arguments")), &args))
	require.Equal(t, "src/main.go", args["path"])
	require.NotContains(t, args, "file_path")
	require.Equal(t, `fmt.Println("hello")`, args["old_string"])
	require.Equal(t, "fmt.Println('hi')", args["new_string"])
}

func TestNormalizeChatCompletionsRequestBodyNormalizesArrayContentBlocks(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.4",
		"messages": [
			{"role":"user","content":["hello",{"text":"world","cache_control":{"type":"ephemeral"}},{"type":"image_url","image_url":{"url":"https://example.com/x.png"}}]}
		]
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	messages := requirePayloadSlice(t, payload, "messages")
	require.Len(t, messages, 1)
	content := requireSliceField(t, requireMapValue(t, messages[0]), "content")
	require.Len(t, content, 3)

	first := requireMapValue(t, content[0])
	require.Equal(t, "text", first["type"])
	require.Equal(t, "hello", first["text"])

	second := requireMapValue(t, content[1])
	require.Equal(t, "text", second["type"])
	require.Equal(t, "world", second["text"])
	_, hasCacheControl := second["cache_control"]
	require.False(t, hasCacheControl)

	third := requireMapValue(t, content[2])
	require.Equal(t, "image_url", third["type"])
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
	tools := requirePayloadSlice(t, payload, "tools")
	require.Len(t, tools, 4)
	toolA := requireMapValue(t, tools[0])
	toolAFn := requireMapField(t, toolA, "function")
	require.Equal(t, "tool_a", toolAFn["name"])
	require.Contains(t, toolAFn, "parameters")
	toolB := requireMapValue(t, tools[1])
	toolBFn := requireMapField(t, toolB, "function")
	require.Equal(t, "tool_b", toolBFn["name"])
	toolC := requireMapValue(t, tools[2])
	toolCFn := requireMapField(t, toolC, "function")
	require.Equal(t, "tool_c", toolCFn["name"])
	require.Equal(t, "Claude style", toolCFn["description"])
	require.Contains(t, toolCFn, "parameters")
	toolD := requireMapValue(t, tools[3])
	require.Equal(t, "web_search", toolD["type"])
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
	messages := requirePayloadSlice(t, payload, "messages")
	system := requireMapValue(t, messages[0])
	content := requireStringField(t, system, "content")
	require.NotContains(t, content, "multi_tool_use.parallel")
	require.Contains(t, content, "multiple tool_calls in the same assistant response")
	require.Len(t, requirePayloadSlice(t, payload, "tools"), 2)
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
	tools := requirePayloadSlice(t, payload, "tools")
	require.Len(t, tools, 1)
	tool := requireMapValue(t, tools[0])
	require.Contains(t, tool, "input_schema")
	messages := requirePayloadSlice(t, payload, "messages")
	content := requireSliceField(t, requireMapValue(t, messages[0]), "content")
	toolUse := requireMapValue(t, content[0])
	require.Equal(t, `{"q":"hello"}`, toolUse["input"])
}
