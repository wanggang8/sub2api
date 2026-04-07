package cursor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeResponsesRequestBodyConvertsChatMessagesPayload(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hello"}],
		"stream":false
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.NotContains(t, payload, "messages")
	require.Contains(t, payload, "input")
}

func TestNormalizeChatCompletionsRequestBodyConvertsResponsesInputPayload(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"input":[{"role":"user","content":"hello"}],
		"stream":true
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.NotContains(t, payload, "input")
	require.Contains(t, payload, "messages")
}

func TestNormalizeChatCompletionsRequestBodyMapsResponsesFields(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"input":[{"role":"user","content":"hello"}],
		"stream":true,
		"max_output_tokens":512,
		"reasoning":{"effort":"high"},
		"tools":[{"type":"function","name":"read_file","description":"Read file","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"read_file"},
		"service_tier":"priority"
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Equal(t, float64(512), payload["max_completion_tokens"])
	require.Equal(t, "high", payload["reasoning_effort"])
	require.Equal(t, "priority", payload["service_tier"])
	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool := tools[0].(map[string]any)
	require.Equal(t, "function", tool["type"])
	function := tool["function"].(map[string]any)
	require.Equal(t, "read_file", function["name"])
	toolChoice := payload["tool_choice"].(map[string]any)
	require.Equal(t, "function", toolChoice["type"])
	functionChoice := toolChoice["function"].(map[string]any)
	require.Equal(t, "read_file", functionChoice["name"])
}

func TestNormalizeChatCompletionsRequestBodyPromotesInstructionsToSystemMessage(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"instructions":"follow the rules",
		"input":"hello",
		"stream":false
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload struct {
		Messages []map[string]any `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Len(t, payload.Messages, 2)
	require.Equal(t, "system", payload.Messages[0]["role"])
	require.Equal(t, "follow the rules", payload.Messages[0]["content"])
	require.Equal(t, "user", payload.Messages[1]["role"])
	require.Equal(t, "hello", payload.Messages[1]["content"])
}

func TestNormalizeChatCompletionsRequestBodyHandlesTypedResponsesInputItems(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"input":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"think first"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"tool preface"}]},
			{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"file content"}
		],
		"stream":false
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload struct {
		Messages []struct {
			Role             string `json:"role"`
			Content          any    `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Len(t, payload.Messages, 2)
	require.Equal(t, "assistant", payload.Messages[0].Role)
	require.Equal(t, "think first", payload.Messages[0].ReasoningContent)
	require.Equal(t, "tool preface", payload.Messages[0].Content)
	require.Len(t, payload.Messages[0].ToolCalls, 1)
	require.Equal(t, "call_1", payload.Messages[0].ToolCalls[0].ID)
	require.Equal(t, "function", payload.Messages[0].ToolCalls[0].Type)
	require.Equal(t, "read_file", payload.Messages[0].ToolCalls[0].Function.Name)
	require.Equal(t, `{"path":"README.md"}`, payload.Messages[0].ToolCalls[0].Function.Arguments)
	require.Equal(t, "tool", payload.Messages[1].Role)
	require.Equal(t, "call_1", payload.Messages[1].ToolCallID)
	require.Equal(t, "file content", payload.Messages[1].Content)
}

func TestNormalizeChatCompletionsRequestBodyKeepsReasoningWhenFunctionCallFollowsImmediately(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"input":[
			{"type":"reasoning","summary":[{"type":"summary_text","text":"think before tool"}]},
			{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}
		],
		"stream":false
	}`)

	normalized, err := NormalizeChatCompletionsRequestBody(raw)
	require.NoError(t, err)

	var payload struct {
		Messages []struct {
			Role             string `json:"role"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID string `json:"id"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Len(t, payload.Messages, 1)
	require.Equal(t, "assistant", payload.Messages[0].Role)
	require.Equal(t, "think before tool", payload.Messages[0].ReasoningContent)
	require.Len(t, payload.Messages[0].ToolCalls, 1)
	require.Equal(t, "call_1", payload.Messages[0].ToolCalls[0].ID)
}

func TestNormalizeResponsesRequestBodyConvertsSystemMessagesToInstructions(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"messages":[
			{"role":"system","content":"follow the rules"},
			{"role":"user","content":"hello"}
		],
		"max_tokens":256,
		"stream":false
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Equal(t, "follow the rules", payload["instructions"])
	require.NotContains(t, payload, "messages")
	require.Equal(t, float64(256), payload["max_output_tokens"])
	input, ok := payload["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 1)
	item := input[0].(map[string]any)
	require.Equal(t, "user", item["role"])
	require.Equal(t, "hello", item["content"])
}

func TestNormalizeResponsesRequestBodyAddsCompatDefaults(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hello"}],
		"max_tokens":64,
		"reasoning_effort":"high",
		"stream":false
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload struct {
		Store     *bool    `json:"store"`
		Include   []string `json:"include"`
		Reasoning struct {
			Effort  string `json:"effort"`
			Summary string `json:"summary"`
		} `json:"reasoning"`
	}
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.NotNil(t, payload.Store)
	require.False(t, *payload.Store)
	require.Contains(t, payload.Include, "reasoning.encrypted_content")
	require.Equal(t, "high", payload.Reasoning.Effort)
	require.Equal(t, "auto", payload.Reasoning.Summary)
}

func TestNormalizeResponsesRequestBodyPreservesLegacyFunctionCallingFields(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"user","content":"hello"}],
		"functions":[{"name":"read_file","description":"Read file","parameters":{"type":"object"}}],
		"function_call":{"name":"read_file"},
		"stream":false
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload struct {
		Tools      []map[string]any `json:"tools"`
		ToolChoice map[string]any   `json:"tool_choice"`
	}
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Len(t, payload.Tools, 1)
	require.Equal(t, "function", payload.Tools[0]["type"])
	require.Equal(t, "read_file", payload.Tools[0]["name"])
	require.Equal(t, "function", payload.ToolChoice["type"])
	function, ok := payload.ToolChoice["function"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "read_file", function["name"])
}

func TestNormalizeResponsesRequestBodyConvertsLegacyFunctionRoleToFunctionCallOutput(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"messages":[{"role":"function","name":"read_file","content":"file content"}],
		"stream":false
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload struct {
		Input []map[string]any `json:"input"`
	}
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Len(t, payload.Input, 1)
	require.Equal(t, "function_call_output", payload.Input[0]["type"])
	require.Equal(t, "read_file", payload.Input[0]["call_id"])
	require.Equal(t, "file content", payload.Input[0]["output"])
}

func TestNormalizeResponsesRequestBodyPreservesAssistantReasoningContent(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"messages":[
			{"role":"assistant","content":"tool preface","reasoning_content":"think first","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]}
		],
		"stream":false
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload struct {
		Input []map[string]any `json:"input"`
	}
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Len(t, payload.Input, 3)
	require.Equal(t, "reasoning", payload.Input[0]["type"])
	summary, ok := payload.Input[0]["summary"].([]any)
	require.True(t, ok)
	require.Len(t, summary, 1)
	summaryItem, ok := summary[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "summary_text", summaryItem["type"])
	require.Equal(t, "think first", summaryItem["text"])
	require.Equal(t, "message", payload.Input[1]["type"])
	require.Equal(t, "function_call", payload.Input[2]["type"])
}

func TestNormalizeResponsesRequestBodyPreservesAssistantLegacyFunctionCallMessage(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"messages":[
			{"role":"assistant","content":"calling tool","function_call":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}},
			{"role":"function","name":"read_file","content":"file content"}
		],
		"stream":false
	}`)

	normalized, err := NormalizeResponsesRequestBody(raw)
	require.NoError(t, err)

	var payload struct {
		Input []map[string]any `json:"input"`
	}
	require.NoError(t, json.Unmarshal(normalized, &payload))
	require.Len(t, payload.Input, 3)
	require.Equal(t, "message", payload.Input[0]["type"])
	require.Equal(t, "function_call", payload.Input[1]["type"])
	require.Equal(t, "read_file", payload.Input[1]["name"])
	require.Equal(t, `{"path":"README.md"}`, payload.Input[1]["arguments"])
	require.Equal(t, "function_call_output", payload.Input[2]["type"])
	require.Equal(t, "read_file", payload.Input[2]["call_id"])
}

func TestNormalizeMessagesRequestBodyPreservesAnthropicPayload(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[{"role":"user","content":"hello"}],
		"thinking":{"type":"enabled","budget_tokens":2048},
		"stream":false
	}`)

	normalized, err := NormalizeMessagesRequestBody(raw)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(normalized))
}

func TestPatchMessagesResponseBodyInjectsThinkingBlock(t *testing.T) {
	raw := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-5",
		"content":[{"type":"text","text":"answer"}],
		"reasoning_content":"think first",
		"stop_reason":"end_turn",
		"usage":{"input_tokens":7,"output_tokens":3}
	}`)

	patched, err := PatchMessagesResponseBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(patched, &payload))
	require.NotContains(t, payload, "reasoning_content")

	content, ok := payload["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 2)

	first, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "thinking", first["type"])
	require.Equal(t, "think first", first["thinking"])

	second, ok := content[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "text", second["type"])
	require.Equal(t, "answer", second["text"])
}

func TestPatchMessagesStreamBundleInjectsThinkingEvents(t *testing.T) {
	bundle := []byte(strings.Join([]string{
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello","reasoningContent":"think"}}`,
		"",
	}, "\n"))

	state := NewMessagesStreamState()
	chunks := splitMessagesSSEBundle(bundle)
	var patched bytes.Buffer
	for _, chunk := range chunks {
		piece, err := PatchMessagesStreamChunk(chunk, state)
		require.NoError(t, err)
		patched.Write(piece)
	}
	patched.Write(FinalizeMessagesStream(state))

	patchedStr := patched.String()
	require.Contains(t, patchedStr, "event: content_block_start")
	require.Contains(t, patchedStr, `"thinking":"think"`)
	require.Contains(t, patchedStr, `"index":1`)
}

func TestPatchMessagesStreamBundleBuffersIndexedEventsUntilThinkingShown(t *testing.T) {
	bundle := []byte(strings.Join([]string{
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello","reasoning_content":"think first"}}`,
		"",
	}, "\n"))

	state := NewMessagesStreamState()
	chunks := splitMessagesSSEBundle(bundle)
	var patched bytes.Buffer
	for _, chunk := range chunks {
		piece, err := PatchMessagesStreamChunk(chunk, state)
		require.NoError(t, err)
		patched.Write(piece)
	}
	patched.Write(FinalizeMessagesStream(state))

	patchedStr := patched.String()
	require.Contains(t, patchedStr, "event: content_block_start\ndata: {\"content_block\":{\"thinking\":\"\",\"type\":\"thinking\"},\"index\":0,\"type\":\"content_block_start\"}")
	require.Contains(t, patchedStr, "event: content_block_start\ndata: {\"content_block\":{\"text\":\"\",\"type\":\"text\"},\"index\":1,\"type\":\"content_block_start\"}")
	require.Contains(t, patchedStr, "event: content_block_delta\ndata: {\"delta\":{\"text\":\"Hello\",\"type\":\"text_delta\"},\"index\":1,\"type\":\"content_block_delta\"}")
}

func TestPatchResponsesStreamBundlePrefixesCreatedAndOmitsDone(t *testing.T) {
	bundle := []byte(strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed"}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n"))

	state := NewResponsesStreamState()
	chunks := splitMessagesSSEBundle(bundle)
	var patched bytes.Buffer
	for _, chunk := range chunks {
		piece, err := PatchResponsesStreamChunk(chunk, "cursor-model", state)
		require.NoError(t, err)
		patched.Write(piece)
	}

	patchedStr := patched.String()
	require.Contains(t, patchedStr, "event: response.created")
	require.Contains(t, patchedStr, `"model":"cursor-model"`)
	require.Contains(t, patchedStr, "event: response.output_text.done")
	require.Contains(t, patchedStr, "event: response.completed")
	require.NotContains(t, patchedStr, "[DONE]")
	require.Less(t, strings.Index(patchedStr, "event: response.output_text.done"), strings.Index(patchedStr, "event: response.completed"))
}

func TestPatchResponsesStreamBundleReconstructsCompletedOutput(t *testing.T) {
	bundle := []byte(strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","model":"upstream-model"}}`,
		"",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
		"",
		`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"think"}`,
		"",
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","status":"in_progress","role":"assistant","content":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","output_index":1,"content_index":0,"delta":"hello"}`,
		"",
		`data: {"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_1","status":"in_progress","call_id":"call_1","name":"read_file","arguments":""}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"path\":\"README.md\"}"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed"}}`,
		"",
	}, "\n"))

	state := NewResponsesStreamState()
	chunks := splitMessagesSSEBundle(bundle)
	var patched bytes.Buffer
	for _, chunk := range chunks {
		piece, err := PatchResponsesStreamChunk(chunk, "cursor-model", state)
		require.NoError(t, err)
		patched.Write(piece)
	}

	patchedStr := patched.String()
	require.Contains(t, patchedStr, `"summary":[{"text":"think","type":"summary_text"}]`)
	require.Contains(t, patchedStr, `"content":[{"text":"hello","type":"output_text"}]`)
	require.Contains(t, patchedStr, `"name":"read_file"`)
	require.Contains(t, patchedStr, `"arguments":"{\"path\":\"README.md\"}"`)
	require.Contains(t, patchedStr, "event: response.reasoning_summary_text.done\ndata: {\"output_index\":0")
	require.Contains(t, patchedStr, "event: response.output_text.done\ndata: {\"content_index\":0,\"output_index\":1")
	require.Contains(t, patchedStr, "event: response.function_call_arguments.done\ndata: {\"arguments\":")
	require.Contains(t, patchedStr, `"output_index":2`)
}

func TestPatchResponsesStreamBundleDoesNotFinalizeDoneItemsOnFailure(t *testing.T) {
	bundle := []byte(strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","status":"in_progress","role":"assistant","content":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		"",
		`data: {"type":"response.failed","response":{"id":"resp_1","object":"response","status":"failed","error":{"code":"server_error","message":"boom"}}}`,
		"",
	}, "\n"))

	state := NewResponsesStreamState()
	chunks := splitMessagesSSEBundle(bundle)
	var patched bytes.Buffer
	for _, chunk := range chunks {
		piece, err := PatchResponsesStreamChunk(chunk, "cursor-model", state)
		require.NoError(t, err)
		patched.Write(piece)
	}

	patchedStr := patched.String()
	require.Contains(t, patchedStr, "event: response.failed")
	require.NotContains(t, patchedStr, "event: response.output_text.done")
	require.NotContains(t, patchedStr, "event: response.output_item.done")
	require.NotContains(t, patchedStr, `"status":"completed"`)
	require.Contains(t, patchedStr, `"status":"failed"`)
}

func TestNormalizeMessagesRequestBodyRepairsClaudeToolUseInputsAndSchemas(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"messages":[
			{"role":"assistant","content":[
				{"type":"tool_use","id":"toolu_1","name":"read_file","input":null}
			]}
		],
		"tools":[
			{"name":"read_file","description":"Read file"}
		]
	}`)

	normalized, err := NormalizeMessagesRequestBody(raw)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(normalized, &payload))

	tools := payload["tools"].([]any)
	require.Len(t, tools, 1)
	tool := tools[0].(map[string]any)
	inputSchema, ok := tool["input_schema"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "object", inputSchema["type"])
	require.Equal(t, map[string]any{}, inputSchema["properties"])

	messages := payload["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	input, ok := content[0].(map[string]any)["input"].(map[string]any)
	require.True(t, ok)
	require.Empty(t, input)
}
