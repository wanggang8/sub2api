package cursor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPatchChatStreamChunkSplitsThinkTags(t *testing.T) {
	state := NewChatStreamState()
	bundle := []byte("data: {\"id\":\"cmpl_1\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<think>reason</think>Hello\"},\"finish_reason\":null}]}\n\n")

	fixed, err := PatchChatStreamChunk(bundle, "cursor-model", state)
	require.NoError(t, err)

	fixedStr := string(fixed)
	require.Contains(t, fixedStr, `"reasoning_content":"reason"`)
	require.Contains(t, fixedStr, `"content":"Hello"`)
}

func TestPatchChatStreamChunkStopsThinkingBeforeToolCalls(t *testing.T) {
	state := NewChatStreamState()

	chunk1, err := PatchChatStreamChunk([]byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"content":"<think>reason"},"finish_reason":null}]}`+"\n\n"), "cursor-model", state)
	require.NoError(t, err)
	require.Contains(t, string(chunk1), `"reasoning_content":"reason"`)

	chunk2, err := PatchChatStreamChunk([]byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":null}]}`+"\n\n"), "cursor-model", state)
	require.NoError(t, err)
	require.Contains(t, string(chunk2), `"\n\u003c/think\u003e\n\n"`)
	require.Contains(t, string(chunk2), `"tool_calls":[`)

	chunk3, err := PatchChatStreamChunk([]byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`+"\n\n"), "cursor-model", state)
	require.NoError(t, err)
	require.Contains(t, string(chunk3), `"content":"Hello"`)
	require.NotContains(t, string(chunk3), `"reasoning_content":"Hello"`)
}

func TestPatchChatStreamChunkClosesThinkingBeforeDone(t *testing.T) {
	state := NewChatStreamState()
	_, err := PatchChatStreamChunk([]byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"content":"<think>reason"},"finish_reason":null}]}`+"\n\n"), "cursor-model", state)
	require.NoError(t, err)

	fixed, err := PatchChatStreamChunk([]byte("data: [DONE]\n\n"), "cursor-model", state)
	require.NoError(t, err)

	fixedStr := string(fixed)
	closeIdx := strings.Index(fixedStr, `\u003c/think\u003e`)
	if closeIdx == -1 {
		closeIdx = strings.Index(fixedStr, `</think>`)
	}
	doneIdx := strings.Index(fixedStr, `[DONE]`)
	require.NotEqual(t, -1, closeIdx)
	require.NotEqual(t, -1, doneIdx)
	require.Less(t, closeIdx, doneIdx)
}

func TestPatchChatStreamChunkSplitsContentAndToolCallsFromSameChunk(t *testing.T) {
	state := NewChatStreamState()
	state.ChatToolCallsSeen = true
	bundle := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"content":"Hello","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n")

	fixed, err := PatchChatStreamChunk(bundle, "cursor-model", state)
	require.NoError(t, err)

	payloads := decodeChatChunkPayloads(t, fixed)
	require.Len(t, payloads, 2)
	require.Equal(t, "Hello", payloadChoiceDelta(t, payloads[0])["content"])
	require.Nil(t, payloadChoiceDelta(t, payloads[0])["tool_calls"])
	toolCalls := payloadChoiceDelta(t, payloads[1])["tool_calls"].([]any)
	require.Equal(t, "call_1", toolCalls[0].(map[string]any)["id"])
	require.Equal(t, "tool_calls", payloadChoice(t, payloads[1])["finish_reason"])
}

func TestPatchChatStreamChunkDropsEmptyToolCallDelta(t *testing.T) {
	state := NewChatStreamState()
	bundle := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"","arguments":""}}]},"finish_reason":null}]}` + "\n\n")

	fixed, err := PatchChatStreamChunk(bundle, "cursor-model", state)
	require.NoError(t, err)

	payloads := decodeChatChunkPayloads(t, fixed)
	require.Len(t, payloads, 0)
}

func TestPatchChatStreamChunkPreservesEmptyToolCallArgumentsForOpenCall(t *testing.T) {
	state := NewChatStreamState()
	bundle := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"ApplyPatch","arguments":""}}]},"finish_reason":null}]}` + "\n\n")

	fixed, err := PatchChatStreamChunk(bundle, "cursor-model", state)
	require.NoError(t, err)

	payloads := decodeChatChunkPayloads(t, fixed)
	require.Len(t, payloads, 1)
	toolCalls := payloadChoiceDelta(t, payloads[0])["tool_calls"].([]any)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "ApplyPatch", function["name"])
	require.Contains(t, function, "arguments")
	require.Equal(t, "", function["arguments"])
}

func TestPatchChatStreamChunkUnwrapsApplyPatchArguments(t *testing.T) {
	state := NewChatStreamState()
	start := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"ApplyPatch","arguments":""}}]},"finish_reason":null}]}` + "\n\n")
	done := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"patch\":\"*** Begin Patch\\n*** Add File: /tmp/a.txt\\n+hello\\n*** End Patch\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n")

	fixedStart, err := PatchChatStreamChunk(start, "cursor-model", state)
	require.NoError(t, err)
	fixedDone, err := PatchChatStreamChunk(done, "cursor-model", state)
	require.NoError(t, err)

	startPayloads := decodeChatChunkPayloads(t, fixedStart)
	require.Len(t, startPayloads, 1)
	firstToolCalls := payloadChoiceDelta(t, startPayloads[0])["tool_calls"].([]any)
	require.Equal(t, "", firstToolCalls[0].(map[string]any)["function"].(map[string]any)["arguments"])
	donePayloads := decodeChatChunkPayloads(t, fixedDone)
	require.Len(t, donePayloads, 1)
	secondToolCalls := payloadChoiceDelta(t, donePayloads[0])["tool_calls"].([]any)
	function := secondToolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "*** Begin Patch\n*** Add File: /tmp/a.txt\n+hello\n*** End Patch", function["arguments"])
}

func TestPatchChatStreamChunkUnwrapsApplyPatchDeletionArguments(t *testing.T) {
	state := NewChatStreamState()
	start := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"ApplyPatch","arguments":""}}]},"finish_reason":null}]}` + "\n\n")
	done := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"patch\":\"*** Begin Patch\\n*** Update File: /tmp/a.txt\\n@@\\n-old line\\n*** End Patch\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n")

	_, err := PatchChatStreamChunk(start, "cursor-model", state)
	require.NoError(t, err)
	fixedDone, err := PatchChatStreamChunk(done, "cursor-model", state)
	require.NoError(t, err)

	donePayloads := decodeChatChunkPayloads(t, fixedDone)
	require.Len(t, donePayloads, 1)
	toolCalls := payloadChoiceDelta(t, donePayloads[0])["tool_calls"].([]any)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "*** Begin Patch\n*** Update File: /tmp/a.txt\n@@\n-old line\n*** End Patch", function["arguments"])
}

func TestPatchChatStreamChunkBuffersSplitApplyPatchArguments(t *testing.T) {
	state := NewChatStreamState()
	start := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"ApplyPatch","arguments":""}}]},"finish_reason":null}]}` + "\n\n")
	part1 := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"patch\":\"*** Begin"}}]},"finish_reason":null}]}` + "\n\n")
	part2 := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":" Patch\\n*** Add File: /tmp/a.txt\\n+hello\\n*** End Patch\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n")

	_, err := PatchChatStreamChunk(start, "cursor-model", state)
	require.NoError(t, err)
	fixedPart1, err := PatchChatStreamChunk(part1, "cursor-model", state)
	require.NoError(t, err)
	require.Empty(t, decodeChatChunkPayloads(t, fixedPart1))

	fixedPart2, err := PatchChatStreamChunk(part2, "cursor-model", state)
	require.NoError(t, err)
	payloads := decodeChatChunkPayloads(t, fixedPart2)
	require.Len(t, payloads, 1)
	toolCalls := payloadChoiceDelta(t, payloads[0])["tool_calls"].([]any)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "*** Begin Patch\n*** Add File: /tmp/a.txt\n+hello\n*** End Patch", function["arguments"])
}

func TestPatchChatStreamChunkAddsMissingToolCallIndex(t *testing.T) {
	state := NewChatStreamState()
	bundle := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n")

	fixed, err := PatchChatStreamChunk(bundle, "cursor-model", state)
	require.NoError(t, err)

	payloads := decodeChatChunkPayloads(t, fixed)
	require.Len(t, payloads, 1)
	toolCalls := payloadChoiceDelta(t, payloads[0])["tool_calls"].([]any)
	toolCall := toolCalls[0].(map[string]any)
	require.Equal(t, float64(0), toolCall["index"])
	require.Equal(t, "function", toolCall["type"])
	require.NotEmpty(t, toolCall["id"])
}

func TestPatchChatStreamChunkDropsEmptyToolCallIdentityFields(t *testing.T) {
	state := NewChatStreamState()
	bundle := []byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"","type":"","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}` + "\n\n")

	fixed, err := PatchChatStreamChunk(bundle, "cursor-model", state)
	require.NoError(t, err)

	payloads := decodeChatChunkPayloads(t, fixed)
	require.Len(t, payloads, 1)
	toolCalls := payloadChoiceDelta(t, payloads[0])["tool_calls"].([]any)
	toolCall := toolCalls[0].(map[string]any)
	_, hasID := toolCall["id"]
	require.False(t, hasID)
	require.Equal(t, float64(0), toolCall["index"])
	require.Equal(t, "function", toolCall["type"])
}

func decodeChatChunkPayloads(t *testing.T, bundle []byte) []map[string]any {
	t.Helper()

	chunks := splitChatTestSSEBundle(bundle)
	payloads := make([]map[string]any, 0, len(chunks))
	for _, chunk := range chunks {
		_, data, ok := parseMessagesSSEChunk(chunk)
		if !ok || data == "[DONE]" {
			continue
		}
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(data), &payload))
		payloads = append(payloads, payload)
	}
	return payloads
}

func splitChatTestSSEBundle(bundle []byte) [][]byte {
	if len(bundle) == 0 {
		return nil
	}
	normalized := bytes.ReplaceAll(bundle, []byte("\r\n"), []byte("\n"))
	rawChunks := bytes.Split(normalized, []byte("\n\n"))
	chunks := make([][]byte, 0, len(rawChunks))
	for _, raw := range rawChunks {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		chunk := raw
		if !bytes.HasSuffix(chunk, []byte("\n\n")) {
			chunk = append(chunk, []byte("\n\n")...)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func payloadChoice(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	choices := payload["choices"].([]any)
	return choices[0].(map[string]any)
}

func payloadChoiceDelta(t *testing.T, payload map[string]any) map[string]any {
	t.Helper()
	return payloadChoice(t, payload)["delta"].(map[string]any)
}
