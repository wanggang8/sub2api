package cursor

import (
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
