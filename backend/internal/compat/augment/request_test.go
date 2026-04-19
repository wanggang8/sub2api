package augment

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestNormalizeChatStreamRequestBodyBuildsAnthropicMessagesRequest(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"message":"review this function",
		"stream":true,
		"tool_definitions":[{
			"name":"read_file",
			"description":"Read a file",
			"parameters":{"type":"object","properties":{"path":{"type":"string"}}}
		}],
		"request_nodes":[
			{"type":0,"text_node":{"text":"selected code"}},
			{"type":1,"tool_result_node":{"tool_use_id":"tool_1","content":"tool output"}}
		],
		"chat_history":[{
			"request_message":"hi",
			"response_text":"hello"
		}],
		"enable_thinking":true,
		"max_tokens":2000
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.Equal(t, "claude-sonnet-4-5", req.Model)
	require.True(t, req.Stream)
	require.Equal(t, 2000, req.MaxTokens)
	require.Len(t, req.Messages, 3)
	require.Len(t, req.Tools, 1)
	require.NotNil(t, req.Thinking)
	require.Equal(t, "enabled", req.Thinking.Type)
	require.NotEmpty(t, req.ToolChoice)
}

func TestNormalizeChatStreamRequestBodyAcceptsCamelCaseStructuredPayload(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"prompt":"review this function",
		"requestNodes":[
			{"type":0,"textNode":{"text":"selected code"}}
		],
		"chatHistory":[{
			"requestMessage":"hi",
			"responseText":"hello",
			"responseNodes":[
				{"type":5,"toolUse":{"toolUseId":"tool_1","toolName":"read_file","inputJson":"{\"path\":\"README.md\"}"}}
			]
		}],
		"toolDefinitions":[{
			"name":"read_file",
			"description":"Read a file",
			"inputSchema":{"type":"object","properties":{"path":{"type":"string"}}}
		}],
		"enableThinking":true,
		"maxTokens":2048,
		"selectedCode":"fmt.Println(1)",
		"path":"/repo/main.go",
		"stream":false
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.Equal(t, "claude-sonnet-4-5", req.Model)
	require.False(t, req.Stream)
	require.Equal(t, 2048, req.MaxTokens)
	require.Len(t, req.Tools, 1)
	require.NotNil(t, req.Thinking)
	require.Len(t, req.Messages, 3)

	var assistantBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[1].Content, &assistantBlocks))
	require.Len(t, assistantBlocks, 2)
	require.Equal(t, "tool_use", assistantBlocks[0]["type"])
	require.Equal(t, "tool_1", assistantBlocks[0]["id"])
	require.Equal(t, "read_file", assistantBlocks[0]["name"])
	require.Equal(t, "text", assistantBlocks[1]["type"])
	require.Equal(t, "hello", assistantBlocks[1]["text"])

	var systemBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.System, &systemBlocks))
	require.Contains(t, systemBlocks[0]["text"], "[path]\n/repo/main.go")
	require.Contains(t, systemBlocks[0]["text"], "[selected_code]\nfmt.Println(1)")
}

func TestNormalizeChatStreamRequestBodyDefaultsStreamAndMaxTokens(t *testing.T) {
	raw := []byte(`{"model":"claude-haiku-4-5","message":"hello"}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.True(t, req.Stream)
	require.Equal(t, 32000, req.MaxTokens)
	require.Len(t, req.Messages, 1)
}

func TestNormalizeChatStreamRequestBodyDoesNotDuplicateContextIntoUserMessage(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"message":"review this",
		"path":"/repo/main.go",
		"selected_code":"fmt.Println(1)",
		"stream":true
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.Len(t, req.Messages, 1)
	require.NotEmpty(t, req.System)

	var systemBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.System, &systemBlocks))
	systemText := systemBlocks[0]["text"].(string)
	require.Contains(t, systemText, "[path]\n/repo/main.go")
	require.Contains(t, systemText, "[selected_code]\nfmt.Println(1)")

	var userBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[0].Content, &userBlocks))
	userText := userBlocks[0]["text"].(string)
	require.Contains(t, userText, "review this")
	require.NotContains(t, userText, "[path]\n/repo/main.go")
	require.NotContains(t, userText, "[selected_code]\nfmt.Println(1)")
}

func TestNormalizeChatStreamRequestDecryptsEncryptedPayload(t *testing.T) {
	plaintext := []byte(`{"model":"claude-sonnet-4-5","message":"secret prompt","stream":false}`)
	raw := encryptAugmentPayloadWithEmbeddedKey(t, plaintext)

	normalized, stream, err := NormalizeChatStreamRequest(raw)
	require.NoError(t, err)
	require.False(t, stream)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.Equal(t, "claude-sonnet-4-5", req.Model)
	require.False(t, req.Stream)
	require.Len(t, req.Messages, 1)

	var blocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[0].Content, &blocks))
	require.Equal(t, "secret prompt", blocks[0]["text"])
}

func TestNormalizeChatStreamRequestRejectsOversizedEncryptedCiphertext(t *testing.T) {
	raw := encryptAugmentPayloadWithEmbeddedKey(t, []byte(strings.Repeat("a", maxAugmentDecryptedBodyBytes+aes.BlockSize)))
	_, _, err := NormalizeChatStreamRequest(raw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encrypted payload exceeds limit")
}

func TestNormalizeChatStreamRequestRejectsOversizedEncryptedEnvelopeField(t *testing.T) {
	body, err := json.Marshal(map[string]any{
		"encrypted_data": strings.Repeat("A", maxAugmentEncryptedFieldBytes+1),
		"iv":             base64.StdEncoding.EncodeToString([]byte("tiny")),
	})
	require.NoError(t, err)
	_, _, err = NormalizeChatStreamRequest(body)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encrypted payload exceeds limit")
}

func TestNormalizeChatStreamRequestRejectsInvalidNonStructuredPayload(t *testing.T) {
	_, _, err := NormalizeChatStreamRequest([]byte(`{"foo":"bar"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid request format")
}

func TestNormalizeChatStreamRequestBodyRepairsPendingToolUsePairs(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"chat_history":[
			{
				"request_message":"please inspect the file",
				"response_nodes":[
					{"type":5,"tool_use":{"tool_use_id":"call_1","tool_name":"read_file","input_json":"{\"path\":\"README.md\"}"}}
				]
			}
		],
		"message":"continue",
		"stream":false
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.Len(t, req.Messages, 3)
	require.Equal(t, "user", req.Messages[2].Role)

	var toolResultBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[2].Content, &toolResultBlocks))
	require.Len(t, toolResultBlocks, 2)
	require.Equal(t, "tool_result", toolResultBlocks[0]["type"])
	require.Equal(t, "call_1", toolResultBlocks[0]["tool_use_id"])
	require.Equal(t, true, toolResultBlocks[0]["is_error"])
	require.Equal(t, "text", toolResultBlocks[1]["type"])
	require.Equal(t, "continue", toolResultBlocks[1]["text"])
}

func TestNormalizeChatStreamRequestBodyRendersBYOKContextNodesIntoPrompt(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"message":"current request",
		"request_nodes":[
			{"type":4,"ide_state_node":{
				"workspace_folders":[{"repository_root":"/repo","folder_root":"/repo"}],
				"current_terminal":{"terminal_id":2,"current_working_directory":"/repo"}
			}},
			{"type":8,"file_node":{"file_data":"SGVsbG8=","format":"text/plain"}},
			{"type":9,"file_id_node":{"file_id":"fid_1","file_name":"README.md"}},
			{"type":3,"image_id_node":{"image_id":"img_1","format":2}},
			{"type":10,"history_summary_node":{
				"message_template":"Summary: {summary}\nTail: {end_part_full}",
				"summary_text":"older context",
				"history_end":[{"request_message":"tail request","response_text":"tail response"}]
			}}
		],
		"stream":false
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.Len(t, req.Messages, 1)

	var blocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[0].Content, &blocks))
	require.Len(t, blocks, 1)
	require.Equal(t, "text", blocks[0]["type"])
	text := blocks[0]["text"].(string)
	require.Contains(t, text, "current request")
	require.Contains(t, text, "[IDE_STATE]")
	require.Contains(t, text, "[FILE]")
	require.Contains(t, text, "[FILE_ID]")
	require.Contains(t, text, "[IMAGE_ID]")
	require.Contains(t, text, "Summary: older context")
	require.Contains(t, text, "tail request")
	require.Contains(t, text, "tail response")
}

func TestNormalizeChatStreamRequestBodyRendersHistorySummaryThinking(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"request_nodes":[
			{"type":10,"history_summary_node":{
				"message_template":"Summary: {summary}\nTail: {end_part_full}",
				"summary_text":"older context",
				"history_end":[{
					"request_message":"tail request",
					"response_nodes":[
						{"type":8,"thinking":{"summary":"first think"}},
						{"type":0,"text_node":{"text":"tail response"}}
					]
				}]
			}}
		],
		"stream":false
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.Len(t, req.Messages, 1)

	var blocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[0].Content, &blocks))
	require.Len(t, blocks, 1)
	require.Equal(t, "text", blocks[0]["type"])
	text := blocks[0]["text"].(string)
	require.Contains(t, text, "Summary: older context")
	require.Contains(t, text, "tail request")
	require.Contains(t, text, "<thinking>")
	require.Contains(t, text, "first think")
	require.Contains(t, text, "</thinking>")
	require.Contains(t, text, "tail response")
}

func TestNormalizeChatStreamRequestBodyTreatsType2AsTextOrImage(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"chat_history":[
			{
				"request_message":"hi",
				"response_nodes":[
					{"type":2,"text_node":{"text":"main_text_finished"}}
				]
			}
		],
		"request_nodes":[
			{"type":2,"image_node":{"image_data":"aGVsbG8=","format":2}}
		],
		"stream":false
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.Len(t, req.Messages, 3)

	var assistantBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[1].Content, &assistantBlocks))
	require.Len(t, assistantBlocks, 1)
	require.Equal(t, "text", assistantBlocks[0]["type"])
	require.Equal(t, "main_text_finished", assistantBlocks[0]["text"])

	var userBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[2].Content, &userBlocks))
	require.Len(t, userBlocks, 1)
	require.Equal(t, "image", userBlocks[0]["type"])
	source := userBlocks[0]["source"].(map[string]any)
	require.Equal(t, "image/jpeg", source["media_type"])
}

func TestNormalizeChatStreamRequestBodyForwardsTopLevelImages(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"message":"describe this",
		"images":["data:image/jpeg;base64,aGVsbG8="],
		"stream":false
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.Len(t, req.Messages, 1)

	var blocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[0].Content, &blocks))
	require.Len(t, blocks, 2)
	require.Equal(t, "text", blocks[0]["type"])
	require.Equal(t, "describe this", blocks[0]["text"])
	require.Equal(t, "image", blocks[1]["type"])
	source := blocks[1]["source"].(map[string]any)
	require.Equal(t, "image/jpeg", source["media_type"])
	require.Equal(t, "aGVsbG8=", source["data"])
}

func TestNormalizeChatStreamRequestBodyHonorsDisableSelectedCodeDetails(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"message":"review this",
		"path":"/repo/main.go",
		"lang":"go",
		"prefix":"func main() {\n",
		"selected_code":"fmt.Println(1)",
		"suffix":"\n}",
		"diff":"-old\n+new",
		"disable_selected_code_details":true,
		"stream":false
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.NotEmpty(t, req.System)

	var systemBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.System, &systemBlocks))
	systemText := systemBlocks[0]["text"].(string)
	require.Contains(t, systemText, "[path]\n/repo/main.go")
	require.Contains(t, systemText, "[lang]\ngo")
	require.NotContains(t, systemText, "[prefix]")
	require.NotContains(t, systemText, "[selected_code]")
	require.NotContains(t, systemText, "[suffix]")
	require.NotContains(t, systemText, "[diff]")

	var userBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[0].Content, &userBlocks))
	userText := userBlocks[0]["text"].(string)
	require.Equal(t, "review this", userText)
}

func TestNormalizeChatStreamRequestBodyAddsSystemPromptFields(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"message":"review this",
		"persona_type":3,
		"rules":["rule one","rule two"],
		"mode":"AGENT",
		"user_guidelines":"follow user guidance",
		"workspace_guidelines":"follow workspace guidance",
		"stream":false
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.NotEmpty(t, req.System)

	var systemBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.System, &systemBlocks))
	systemText := systemBlocks[0]["text"].(string)
	require.Contains(t, systemText, "Persona: REVIEWER")
	require.Contains(t, systemText, "follow user guidance")
	require.Contains(t, systemText, "follow workspace guidance")
	require.Contains(t, systemText, "rule one\nrule two")
	require.Contains(t, systemText, "You are an AI coding assistant with access to tools. Use tools when needed to complete tasks.")
}

func TestNormalizeChatStreamRequestBodyExtractsAgentMemoriesFromItems(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"message":"review this",
		"memories_info":{"items":["memory one","memory two"]},
		"stream":false
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.NotEmpty(t, req.System)

	var systemBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.System, &systemBlocks))
	systemText := systemBlocks[0]["text"].(string)
	require.Contains(t, systemText, "memory one")
	require.Contains(t, systemText, "memory two")
}

func TestNormalizeChatStreamRequestBodyEnsuresFirstMessageIsUser(t *testing.T) {
	raw := []byte(`{
		"model":"claude-sonnet-4-5",
		"chat_history":[
			{"response_text":"assistant only"}
		],
		"stream":false
	}`)

	normalized, err := NormalizeChatStreamRequestBody(raw)
	require.NoError(t, err)

	var req apicompat.AnthropicRequest
	require.NoError(t, json.Unmarshal(normalized, &req))
	require.Len(t, req.Messages, 2)
	require.Equal(t, "user", req.Messages[0].Role)
	require.Equal(t, "assistant", req.Messages[1].Role)

	var firstBlocks []map[string]any
	require.NoError(t, json.Unmarshal(req.Messages[0].Content, &firstBlocks))
	require.Len(t, firstBlocks, 1)
	require.Equal(t, "text", firstBlocks[0]["type"])
	text, _ := firstBlocks[0]["text"].(string)
	require.Equal(t, "", text)
}

func TestPrepareChatStreamOpenAIResponsesRequestBuildsResponsesInput(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"message":"continue",
		"stream":false,
		"user_guidelines":"be concise",
		"tool_definitions":[{
			"name":"read_file",
			"description":"Read a file",
			"parameters":{"type":"object","properties":{"path":{"type":"string"}}},
			"mcp_server_name":"workspace",
			"mcp_tool_name":"read_file"
		}],
		"chat_history":[{
			"request_message":"inspect README",
			"response_text":"I'll inspect it",
			"response_nodes":[
				{"type":5,"tool_use":{"tool_use_id":"call_hist","tool_name":"read_file","input_json":"{\"path\":\"README.md\"}"}}
			]
		}],
		"request_nodes":[
			{"type":1,"tool_result_node":{"tool_use_id":"call_hist","content":"README contents"}},
			{"type":0,"text_node":{"text":"selected code"}}
		],
		"feature_detection_flags":{"support_tool_use_start":true},
		"enable_thinking":true,
		"max_tokens":2048
	}`)

	prepared, err := PrepareChatStreamOpenAIResponsesRequest(raw)
	require.NoError(t, err)
	require.False(t, prepared.Stream)
	require.True(t, prepared.SupportToolUseStart)
	require.Contains(t, prepared.ToolMetaByName, "read_file")
	require.Equal(t, "workspace", prepared.ToolMetaByName["read_file"].MCPServerName)
	require.Equal(t, "read_file", prepared.ToolMetaByName["read_file"].MCPToolName)

	var req apicompat.ResponsesRequest
	require.NoError(t, json.Unmarshal(prepared.Body, &req))
	require.Equal(t, "gpt-5", req.Model)
	require.False(t, req.Stream)
	require.NotNil(t, req.MaxOutputTokens)
	require.Equal(t, 2048, *req.MaxOutputTokens)
	require.NotNil(t, req.Reasoning)
	require.Equal(t, "high", req.Reasoning.Effort)
	require.Contains(t, req.Include, "reasoning.encrypted_content")
	require.Len(t, req.Tools, 1)
	require.Equal(t, "function", req.Tools[0].Type)
	require.NotNil(t, req.Tools[0].Strict)
	require.True(t, *req.Tools[0].Strict)
	require.Contains(t, req.Instructions, "be concise")

	var items []map[string]any
	require.NoError(t, json.Unmarshal(req.Input, &items))
	require.Len(t, items, 5)

	require.Equal(t, "message", items[0]["type"])
	require.Equal(t, "user", items[0]["role"])
	require.Equal(t, "inspect README", items[0]["content"])

	require.Equal(t, "message", items[1]["type"])
	require.Equal(t, "assistant", items[1]["role"])
	require.Equal(t, "I'll inspect it", items[1]["content"])

	require.Equal(t, "function_call", items[2]["type"])
	require.Equal(t, "call_hist", items[2]["call_id"])
	require.Equal(t, "read_file", items[2]["name"])
	require.JSONEq(t, `{"path":"README.md"}`, items[2]["arguments"].(string))

	require.Equal(t, "function_call_output", items[3]["type"])
	require.Equal(t, "call_hist", items[3]["call_id"])
	require.Equal(t, "README contents", items[3]["output"])

	require.Equal(t, "message", items[4]["type"])
	require.Equal(t, "user", items[4]["role"])
	currentContent, ok := items[4]["content"].(string)
	require.True(t, ok)
	require.Contains(t, currentContent, "continue")
	require.Contains(t, currentContent, "selected code")
}

func TestPrepareChatStreamOpenAIResponsesRequestRepairsPendingToolCalls(t *testing.T) {
	raw := []byte(`{
		"model":"gpt-5",
		"message":"continue",
		"stream":false,
		"chat_history":[
			{
				"request_message":"inspect README",
				"response_nodes":[
					{"type":5,"tool_use":{"tool_use_id":"call_hist","tool_name":"read_file","input_json":"{\"path\":\"README.md\"}"}}
				]
			}
		]
	}`)

	prepared, err := PrepareChatStreamOpenAIResponsesRequest(raw)
	require.NoError(t, err)

	var req apicompat.ResponsesRequest
	require.NoError(t, json.Unmarshal(prepared.Body, &req))

	var items []map[string]any
	require.NoError(t, json.Unmarshal(req.Input, &items))
	require.Len(t, items, 4)

	require.Equal(t, "message", items[0]["type"])
	require.Equal(t, "user", items[0]["role"])
	require.Equal(t, "inspect README", items[0]["content"])

	require.Equal(t, "function_call", items[1]["type"])
	require.Equal(t, "call_hist", items[1]["call_id"])
	require.Equal(t, "read_file", items[1]["name"])

	require.Equal(t, "function_call_output", items[2]["type"])
	require.Equal(t, "call_hist", items[2]["call_id"])
	require.Contains(t, items[2]["output"], "tool_result not available")

	require.Equal(t, "message", items[3]["type"])
	require.Equal(t, "user", items[3]["role"])
	require.Equal(t, "continue", items[3]["content"])
}

func encryptAugmentPayloadWithEmbeddedKey(t *testing.T, plaintext []byte) []byte {
	t.Helper()

	block, _ := pem.Decode([]byte(embeddedAugmentPrivateKeyPEM))
	require.NotNil(t, block)

	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	require.NoError(t, err)

	privateKey, ok := keyAny.(*rsa.PrivateKey)
	require.True(t, ok)

	aesKey := make([]byte, 32)
	aesIV := make([]byte, aes.BlockSize)
	_, err = rand.Read(aesKey)
	require.NoError(t, err)
	_, err = rand.Read(aesIV)
	require.NoError(t, err)

	blockCipher, err := aes.NewCipher(aesKey)
	require.NoError(t, err)
	padded := pkcs7PadForTest(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(blockCipher, aesIV).CryptBlocks(ciphertext, padded)

	keyIV := append(append([]byte{}, aesKey...), aesIV...)
	encryptedKeyIV, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &privateKey.PublicKey, keyIV, nil)
	require.NoError(t, err)

	body, err := json.Marshal(map[string]any{
		"encrypted_data": base64.StdEncoding.EncodeToString(ciphertext),
		"iv":             base64.StdEncoding.EncodeToString(encryptedKeyIV),
	})
	require.NoError(t, err)
	return body
}

func pkcs7PadForTest(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padded := make([]byte, len(data)+padding)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	return padded
}

func NormalizeChatStreamRequestBody(raw []byte) ([]byte, error) {
	normalized, _, err := NormalizeChatStreamRequest(raw)
	return normalized, err
}
