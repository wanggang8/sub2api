package cursor

import (
	"encoding/json"
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

	choice, message := requirePatchedChatChoice(t, payload)
	require.NotEmpty(t, message["reasoning_content"])
	_, hasLegacy := message["function_call"]
	require.False(t, hasLegacy)

	toolCalls, ok := message["tool_calls"].([]any)
	require.True(t, ok)
	require.Len(t, toolCalls, 1)
	toolCall, ok := toolCalls[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", toolCall["type"])
	require.NotEmpty(t, toolCall["id"])
	require.Equal(t, float64(0), toolCall["index"])
	function, ok := toolCall["function"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "read_file", function["name"])
	require.Equal(t, "tool_calls", choice["finish_reason"])
}

func TestPatchChatResponseBodyNormalizesToolArguments(t *testing.T) {
	filePath := "/workspace/sample.txt"
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
	_, message := requirePatchedChatChoice(t, payload)
	function := requireFirstToolFunction(t, message)

	var args map[string]any
	arguments, ok := function["arguments"].(string)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(arguments), &args))
	require.Equal(t, filePath, args["path"])
	_, hasFilePath := args["file_path"]
	require.False(t, hasFilePath)
	require.Equal(t, `"old"`, args["old_string"])
	require.Equal(t, `"new"`, args["new_string"])
}

func TestPatchChatResponseBodyUnwrapsApplyPatchArguments(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl_patch",
		"choices":[
			{
				"index":0,
				"message":{
					"role":"assistant",
					"tool_calls":[
						{
							"id":"call_patch",
							"type":"function",
							"function":{
								"name":"ApplyPatch",
								"arguments":"{\"patch\":\"*** Begin Patch\\n*** Add File: /tmp/a.txt\\n+hello\\n*** End Patch\"}"
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
	_, message := requirePatchedChatChoice(t, payload)
	function := requireFirstToolFunction(t, message)
	require.Equal(t, "ApplyPatch", function["name"])
	require.Equal(t, "*** Begin Patch\n*** Add File: /tmp/a.txt\n+hello\n*** End Patch", function["arguments"])
}

func requirePatchedChatChoice(t *testing.T, payload map[string]any) (map[string]any, map[string]any) {
	t.Helper()
	choices, ok := payload["choices"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, choices)
	choice, ok := choices[0].(map[string]any)
	require.True(t, ok)
	message, ok := choice["message"].(map[string]any)
	require.True(t, ok)
	return choice, message
}

func requireFirstToolFunction(t *testing.T, message map[string]any) map[string]any {
	t.Helper()
	toolCalls, ok := message["tool_calls"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, toolCalls)
	toolCall, ok := toolCalls[0].(map[string]any)
	require.True(t, ok)
	function, ok := toolCall["function"].(map[string]any)
	require.True(t, ok)
	return function
}
