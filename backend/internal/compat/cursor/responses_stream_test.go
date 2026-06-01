package cursor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPatchResponsesStreamChunkEmitsCreatedBeforeDelta(t *testing.T) {
	state := NewResponsesStreamState()
	chunk, err := PatchResponsesStreamChunk([]byte("event: response.output_text.delta\ndata: {\"delta\":\"hi\",\"output_index\":0}\n\n"), "gpt-4.1", state)
	require.NoError(t, err)
	require.Contains(t, string(chunk), `event: response.created`)
	require.Contains(t, string(chunk), `"model":"gpt-4.1"`)
	require.Contains(t, string(chunk), `event: response.output_text.delta`)
}

func TestPatchResponsesStreamChunkInjectsCompletedOutput(t *testing.T) {
	state := NewResponsesStreamState()
	_, err := PatchResponsesStreamChunk([]byte("event: response.output_item.added\ndata: {\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]},\"output_index\":0}\n\n"), "gpt-4.1", state)
	require.NoError(t, err)
	_, err = PatchResponsesStreamChunk([]byte("event: response.output_item.done\ndata: {\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]},\"output_index\":0}\n\n"), "gpt-4.1", state)
	require.NoError(t, err)
	chunk, err := PatchResponsesStreamChunk([]byte("event: response.completed\ndata: {\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n"), "gpt-4.1", state)
	require.NoError(t, err)
	require.Contains(t, string(chunk), `event: response.completed`)
	require.Contains(t, string(chunk), `"output":[{`)
	require.Contains(t, string(chunk), `"text":"hello"`)
}

func TestFinalizeResponsesStreamClosesOpenReasoningTextAndToolCall(t *testing.T) {
	state := NewResponsesStreamState()
	_, err := PatchResponsesStreamChunk([]byte("event: response.reasoning_summary_text.delta\ndata: {\"delta\":\"think\",\"output_index\":0}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)
	_, err = PatchResponsesStreamChunk([]byte("event: response.output_text.delta\ndata: {\"delta\":\"answer\",\"output_index\":1}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)
	_, err = PatchResponsesStreamChunk([]byte("event: response.output_item.added\ndata: {\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"Read\"},\"output_index\":2}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)
	_, err = PatchResponsesStreamChunk([]byte("event: response.function_call_arguments.delta\ndata: {\"delta\":\"{\\\"path\\\":\",\"output_index\":2}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)
	_, err = PatchResponsesStreamChunk([]byte("event: response.function_call_arguments.delta\ndata: {\"delta\":\"\\\"main.go\\\"}\",\"output_index\":2}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)

	finalized := string(FinalizeResponsesStream("gpt-5.4", state))
	require.Contains(t, finalized, `event: response.reasoning_summary_text.done`)
	require.Contains(t, finalized, `"text":"think"`)
	require.Contains(t, finalized, `event: response.output_text.done`)
	require.Contains(t, finalized, `"text":"answer"`)
	require.Contains(t, finalized, `event: response.function_call_arguments.done`)
	require.Contains(t, finalized, `"call_id":"call_1"`)
	require.Contains(t, finalized, `"arguments":"{\"path\":\"main.go\"}"`)
	require.Contains(t, finalized, `event: response.output_item.done`)
}

func TestFinalizeResponsesStreamKeepsMultipleToolCallsInOutputIndexOrder(t *testing.T) {
	state := NewResponsesStreamState()
	_, err := PatchResponsesStreamChunk([]byte("event: response.output_item.added\ndata: {\"item\":{\"type\":\"function_call\",\"id\":\"fc_2\",\"call_id\":\"call_2\",\"name\":\"Second\"},\"output_index\":2}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)
	_, err = PatchResponsesStreamChunk([]byte("event: response.function_call_arguments.delta\ndata: {\"delta\":\"{}\",\"output_index\":2}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)
	_, err = PatchResponsesStreamChunk([]byte("event: response.output_item.added\ndata: {\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"First\"},\"output_index\":1}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)
	_, err = PatchResponsesStreamChunk([]byte("event: response.function_call_arguments.delta\ndata: {\"delta\":\"{}\",\"output_index\":1}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)

	finalized := string(FinalizeResponsesStream("gpt-5.4", state))
	first := strings.Index(finalized, `"call_id":"call_1"`)
	second := strings.Index(finalized, `"call_id":"call_2"`)
	require.NotEqual(t, -1, first)
	require.NotEqual(t, -1, second)
	require.Less(t, first, second)
}

func TestFinalizeResponsesStreamDoesNotEmitAfterCompleted(t *testing.T) {
	state := NewResponsesStreamState()
	_, err := PatchResponsesStreamChunk([]byte("event: response.output_text.delta\ndata: {\"delta\":\"done\",\"output_index\":0}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)
	_, err = PatchResponsesStreamChunk([]byte("event: response.completed\ndata: {\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n"), "gpt-5.4", state)
	require.NoError(t, err)

	require.Empty(t, FinalizeResponsesStream("gpt-5.4", state))
}
