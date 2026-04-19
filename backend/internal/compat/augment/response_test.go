package augment

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func readNDJSONLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var obj map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &obj))
		out = append(out, obj)
	}
	require.NoError(t, sc.Err())
	return out
}

func TestConvertAnthropicSSEToNDJSON_TextAndUsage(t *testing.T) {
	sse := "" +
		"event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-5\",\"content\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}\n\n"

	var out strings.Builder
	inputTokens, outputTokens, _, err := StreamConvertSSEToNDJSON(strings.NewReader(sse), &out)
	require.NoError(t, err)
	require.Equal(t, 10, inputTokens)
	require.Equal(t, 5, outputTokens)

	lines := readNDJSONLines(t, out.String())
	require.GreaterOrEqual(t, len(lines), 3)
	require.Equal(t, "Hi", lines[0]["text"])
	require.Contains(t, lines[len(lines)-1], "stop_reason")
}

func TestConvertAnthropicJSONToNDJSON_ToolUse(t *testing.T) {
	body := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-5",
		"content":[
			{"type":"text","text":"done"},
			{"type":"tool_use","id":"tool_1","name":"read_file","input":{"path":"README.md"}}
		],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":7,"output_tokens":3}
	}`)

	var out strings.Builder
	inputTokens, outputTokens, _, err := ConvertJSONToNDJSON(body, &out)
	require.NoError(t, err)
	require.Equal(t, 7, inputTokens)
	require.Equal(t, 3, outputTokens)

	lines := readNDJSONLines(t, out.String())
	require.GreaterOrEqual(t, len(lines), 3)
	require.Equal(t, "done", lines[0]["text"])
	require.Contains(t, out.String(), `"tool_name":"read_file"`)
}

func TestConvertAnthropicSSEToNDJSON_PreservesThinkingSignature(t *testing.T) {
	sse := "" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"plan\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig_123\"}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	var out strings.Builder
	_, _, _, err := StreamConvertSSEToNDJSON(strings.NewReader(sse), &out)
	require.NoError(t, err)
	require.Contains(t, out.String(), `"signature":"sig_123"`)
}

func TestConvertOpenAIResponsesJSONToNDJSONIncludesReasoningAndToolMeta(t *testing.T) {
	body := []byte(`{
		"id":"resp_1",
		"object":"response",
		"model":"gpt-5",
		"status":"completed",
		"output":[
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"enc_123"},
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}","status":"completed"},
			{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"done"}],"status":"completed"}
		],
		"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18,"input_tokens_details":{"cached_tokens":2}}
	}`)

	var out strings.Builder
	inputTokens, outputTokens, usageEmitted, err := ConvertOpenAIResponsesJSONToNDJSON(body, &out, OpenAIResponsesConvertOptions{
		ToolMetaByName: map[string]ToolMetadata{
			"read_file": {MCPServerName: "workspace", MCPToolName: "read_file"},
		},
		SupportToolUseStart: true,
	})
	require.NoError(t, err)
	require.Equal(t, 11, inputTokens)
	require.Equal(t, 7, outputTokens)
	require.True(t, usageEmitted)

	lines := readNDJSONLines(t, out.String())
	require.GreaterOrEqual(t, len(lines), 5)
	require.Equal(t, "done", lines[0]["text"])
	require.Contains(t, out.String(), `"summary":"plan"`)
	require.Contains(t, out.String(), `"encrypted_content":"enc_123"`)
	require.Contains(t, out.String(), `"tool_name":"read_file"`)
	require.Contains(t, out.String(), `"mcp_server_name":"workspace"`)
	require.Contains(t, out.String(), `"cache_read_input_tokens":2`)
	require.Contains(t, out.String(), `"stop_reason":3`)
}

func TestConvertOpenAIResponsesJSONToNDJSONMapsIncompleteToMaxTokens(t *testing.T) {
	body := []byte(`{
		"id":"resp_1",
		"object":"response",
		"model":"gpt-5",
		"status":"incomplete",
		"incomplete_details":{"reason":"max_output_tokens"},
		"output":[
			{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"truncated"}],"status":"incomplete"}
		]
	}`)

	var out strings.Builder
	_, _, _, err := ConvertOpenAIResponsesJSONToNDJSON(body, &out, OpenAIResponsesConvertOptions{})
	require.NoError(t, err)
	require.Contains(t, out.String(), `"text":"truncated"`)
	require.Contains(t, out.String(), `"stop_reason":2`)
}

func TestStreamConvertOpenAIResponsesSSEToNDJSONHandlesTextReasoningAndToolCalls(t *testing.T) {
	sse := "" +
		"event: response.output_item.added\n" +
		"data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"read_file\",\"arguments\":\"\"}}\n\n" +
		"event: response.function_call_arguments.delta\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"{\\\"path\\\":\\\"README\"}\n\n" +
		"event: response.function_call_arguments.done\n" +
		"data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":1,\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\"}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"hello\"}\n\n" +
		"event: response.reasoning_summary_text.done\n" +
		"data: {\"type\":\"response.reasoning_summary_text.done\",\"text\":\"plan\"}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"output_text\":\"hello\",\"usage\":{\"input_tokens\":9,\"output_tokens\":4,\"total_tokens\":13}}}\n\n"

	var out strings.Builder
	inputTokens, outputTokens, usageEmitted, err := StreamConvertOpenAIResponsesSSEToNDJSON(strings.NewReader(sse), &out, OpenAIResponsesConvertOptions{
		ToolMetaByName: map[string]ToolMetadata{
			"read_file": {MCPServerName: "workspace", MCPToolName: "read_file"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, 9, inputTokens)
	require.Equal(t, 4, outputTokens)
	require.True(t, usageEmitted)
	require.Contains(t, out.String(), `"text":"hello"`)
	require.Contains(t, out.String(), `"summary":"plan"`)
	require.Contains(t, out.String(), `"tool_use_id":"call_1"`)
	require.Contains(t, out.String(), `"mcp_tool_name":"read_file"`)
	require.Contains(t, out.String(), `"stop_reason":3`)
}

func TestConvertOpenAIResponsesJSONToNDJSONSkipsToolUseStartWhenUnsupported(t *testing.T) {
	body := []byte(`{
		"id":"resp_1",
		"object":"response",
		"model":"gpt-5",
		"status":"completed",
		"output":[
			{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}","status":"completed"},
			{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"done"}],"status":"completed"}
		]
	}`)

	var out strings.Builder
	_, _, _, err := ConvertOpenAIResponsesJSONToNDJSON(body, &out, OpenAIResponsesConvertOptions{
		ToolMetaByName: map[string]ToolMetadata{
			"read_file": {MCPServerName: "workspace", MCPToolName: "read_file"},
		},
		SupportToolUseStart: false,
	})
	require.NoError(t, err)
	require.Contains(t, out.String(), `"tool_use_id":"call_1"`)
	require.NotContains(t, out.String(), `"type":7`)
}

func TestStreamConvertOpenAIResponsesSSEToNDJSONMapsIncompleteEventToMaxTokens(t *testing.T) {
	sse := "" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"partial\"}\n\n" +
		"event: response.incomplete\n" +
		"data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":5,\"output_tokens\":2,\"total_tokens\":7}}}\n\n"

	var out strings.Builder
	inputTokens, outputTokens, usageEmitted, err := StreamConvertOpenAIResponsesSSEToNDJSON(strings.NewReader(sse), &out, OpenAIResponsesConvertOptions{})
	require.NoError(t, err)
	require.Equal(t, 5, inputTokens)
	require.Equal(t, 2, outputTokens)
	require.True(t, usageEmitted)
	require.Contains(t, out.String(), `"text":"partial"`)
	require.Contains(t, out.String(), `"stop_reason":2`)
}

func TestStreamConvertOpenAIResponsesSSEToNDJSONReturnsErrorOnFailedEvent(t *testing.T) {
	sse := "" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"delta\":\"partial\"}\n\n" +
		"event: response.failed\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"failed\",\"error\":{\"message\":\"upstream rejected request\"}}}\n\n"

	var out strings.Builder
	_, _, _, err := StreamConvertOpenAIResponsesSSEToNDJSON(strings.NewReader(sse), &out, OpenAIResponsesConvertOptions{})
	require.ErrorContains(t, err, "upstream rejected request")
	require.Contains(t, out.String(), `"text":"partial"`)
	require.NotContains(t, out.String(), `"stop_reason"`)
}
