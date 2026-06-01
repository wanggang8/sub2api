package cursor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPatchMessagesStreamChunkInjectsThinkingAndShiftsIndex(t *testing.T) {
	state := NewMessagesStreamState()

	chunk1, err := PatchMessagesStreamChunk([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\",\"reasoning_content\":\"secret\"}}\n\n"), state)
	require.NoError(t, err)
	require.Contains(t, string(chunk1), `event: content_block_start`)
	require.Contains(t, string(chunk1), `"thinking":"secret"`)
	require.Contains(t, string(chunk1), `"index":1`)
}

func TestPatchMessagesStreamChunkFlushesPendingOnDone(t *testing.T) {
	state := NewMessagesStreamState()

	chunk1, err := PatchMessagesStreamChunk([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"},\"message\":{\"reasoning_content\":\"think\"}}\n\n"), state)
	require.NoError(t, err)
	require.Empty(t, chunk1)

	chunk2, err := PatchMessagesStreamChunk([]byte("data: [DONE]\n\n"), state)
	require.NoError(t, err)
	require.Contains(t, string(chunk2), `event: content_block_start`)
	require.Contains(t, string(chunk2), `"thinking":"think"`)
	require.Contains(t, string(chunk2), `[DONE]`)
}

func TestPatchMessagesStreamChunkFlushesPendingBlocksWithShiftedIndexes(t *testing.T) {
	state := NewMessagesStreamState()

	start, err := PatchMessagesStreamChunk([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"},\"message\":{\"reasoning_content\":\"plan\"}}\n\n"), state)
	require.NoError(t, err)
	require.Empty(t, start)

	delta, err := PatchMessagesStreamChunk([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n"), state)
	require.NoError(t, err)

	out := string(delta)
	require.Contains(t, out, `event: content_block_start`)
	require.Contains(t, out, `"type":"thinking"`)
	require.Contains(t, out, `"thinking":"plan"`)
	require.Contains(t, out, `"content_block":{"text":"","type":"text"}`)
	require.Contains(t, out, `"text":"answer"`)
	require.Contains(t, out, `"index":1`)
	require.NotContains(t, out, `"reasoning_content"`)
	require.NotContains(t, out, `"reasoningContent"`)
}
