package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPatchChatResponseBodyRepairsLegacyFields(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl_1",
		"model":"upstream-model",
		"choices":[
			{
				"index":0,
				"message":{
					"role":"assistant",
					"content":"<think>Reason here</think>Hello",
					"reasoningContent":"Reason field",
					"function_call":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}
				},
				"finish_reason":"function_call"
			}
		]
	}`)

	fixed, err := PatchChatResponseBody(body, "cursor-model")
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(fixed, &payload))
	require.Equal(t, "cursor-model", payload["model"])

	choice := payload["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	require.NotEmpty(t, message["reasoning_content"])
	_, hasLegacy := message["function_call"]
	require.False(t, hasLegacy)

	toolCalls := message["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	toolCall := toolCalls[0].(map[string]any)
	require.Equal(t, "function", toolCall["type"])
	require.NotEmpty(t, toolCall["id"])
	require.Equal(t, float64(0), toolCall["index"])
	require.Equal(t, "read_file", toolCall["function"].(map[string]any)["name"])
	require.Equal(t, "tool_calls", choice["finish_reason"])
}

func TestPatchChatResponseBodyNormalizesToolArguments(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")
	require.NoError(t, os.WriteFile(filePath, []byte(`const x = "old"`), 0o644))

	body := []byte(`{
		"id":"chatcmpl_2",
		"choices":[
			{
				"index":0,
				"message":{
					"role":"assistant",
					"tool_calls":[
						{
							"id":"call_1",
							"function":{
								"name":"str_replace",
								"arguments":"{\"file_path\":\"` + filePath + `\",\"old_string\":\"“old”\",\"new_string\":\"“new”\"}"
							}
						}
					]
				},
				"finish_reason":"tool_calls"
			}
		]
	}`)

	fixed, err := PatchChatResponseBody(body, "cursor-model")
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(fixed, &payload))
	choice := payload["choices"].([]any)[0].(map[string]any)
	message := choice["message"].(map[string]any)
	toolCall := message["tool_calls"].([]any)[0].(map[string]any)
	function := toolCall["function"].(map[string]any)

	var args map[string]any
	require.NoError(t, json.Unmarshal([]byte(function["arguments"].(string)), &args))
	require.Equal(t, filePath, args["path"])
	_, hasFilePath := args["file_path"]
	require.False(t, hasFilePath)
	require.Equal(t, `"old"`, args["old_string"])
	require.Equal(t, `"new"`, args["new_string"])
}
