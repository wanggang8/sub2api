package augment

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

const embeddedAugmentPrivateKeyPEM = `-----BEGIN PRIVATE KEY-----
MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCsvT+UKn34qpuA
GPwMps/2mjXCUbowW8FSH3tE5OZVOnoKq6uEAhX2Zzl5ZLjBo6y10qS3gBsqKLs3
ULrz7YBvJI0RkF54/kLOMvCggy9l0EQnlAzJyJ0xjE9C0rsq+6EksBF7M2bdkDyS
jaxpm/ccW9we5mZHjLoH8KZG4g4OtptdrAFHTRyK2RpTV2NgdScGPnwz5xzifsMR
JstzlQhp0eQ8yjFoukEEXraKPy0MQ2aOitmc37eOU38upxnvebhFchcIoPJwjbAn
rbbiiH4B/GiKvbCU59M4argbVjddjc/b6KY4ORXdHc7LymN2GGi9+5MPWwIcsmLc
h5C/F3xDAgMBAAECggEAAnxjTiG0IUBH3S1+3oV0nLZ62Up7O8CkAHCNQeBF/ypI
jsPlsWvJsSAYT+80kXcdQFHBJs0tclRyBCTqtP0aShF2l2XUw+9HFkW5ZezPqqqS
njvED+tW4zbMR39WAspYQDBXVKJGKrKtjHrym/XmWn5pEx562cMI+3lpr791Ep5A
nQSy0uqh3LI9+L78mggk69ix89gQAo4YTPEF8dSxHm4PwlhJCX6CnAr14O3nRR55
+GaczZzWWdekFM1mDibzGzzrAFPs/qd/KPUo8xtvvOmWb8qk7Jk9id1A/uHLVqHK
Fbo/MJWbgHhO7KK4+KG0oa+eeRLgJ1WuNjW8AiccIQKBgQDrfcX+R/gbCiunl/Lj
47qwpqGAELc65qDbaWNwckhBezmhXZPUcbULoOKAvxQ3bD+4pmYujgPyRgTFHeAj
Hi2mKxAFHfHdg/yyfon3gAkLbJwF4AfDzoj5L3aL1kVGWPsJ22AzxXbedQ8aroml
hfmoK+7mpgUSpvMSsU27YUy/OQKBgQC7yG6L53A/fnpQpqicXtLJqsm1prYqU9wy
/MnwhtPFO0ikKtBRKMbUEMaufq7SXxgq6mOfwHkiUNcpdyHa8XD0RaaPrbV4RCUC
z5ik3e8MbhXhFyVAMah9Nx1uEyQJfHkkSBnrNESO8STQtR5WgOGvmLqAo5liBm1i
z6a8MUibWwKBgD547U+Z9B3oQtCBJPSD84Dtk6aPyKwdhsruWGz6RGTqtc0zMAaJ
68eb9LrG9iwF0ZnAuxbaof1hyd4pIM7wMJgGkIdq/EERxLXtj9hS5RNcyr9cQXMW
lYvVpZNPUq1o6aAhzJGvbutxDoK7jtSUiDiu/v+9R1c9ZvqsgryVAXExAoGAbHrp
geD9s3B5cMYWed89nksPo+TfL6yqdLocXttE05ff6xbgqUIJOtFGNd/xVo6hA4nM
a6lhUTWqVsX/xN/eBP+HrVEImKWlS+5pnDSpuGCQOyyH1IHbeBqy4bglBWXnBdKx
RnM3d+xO/FLlZ8uklTCB7XaVUU+tOXwEMou2CikCgYBN205yKuscZI2Ihtg8L/QZ
hTMO2saD8L4e5u8g9JXN4t7je7fg9JaoIv+2R+qz21BIYIx0yp621gqlumb9bxXW
W1HkkwHfSDZAsjTWLVS4ZoEp+g4W9CAJ4hxWNEzMhvyed39huUgtJOg8jz3FmwWd
K9fEDfGTA4Uk0yBl+AKwlg==
-----END PRIVATE KEY-----`

const (
	maxAugmentEncryptedFieldBytes = 6 * 1024 * 1024
	maxAugmentDecryptedBodyBytes  = 4 * 1024 * 1024
)

var imageFormatMediaTypes = map[int]string{1: "image/png", 2: "image/jpeg", 3: "image/gif", 4: "image/webp"}

const defaultImageMediaType = "image/png"

type Request struct {
	Model                      string                 `json:"model,omitempty"`
	Message                    string                 `json:"message,omitempty"`
	Nodes                      []Node                 `json:"nodes,omitempty"`
	StructuredRequestNodes     []Node                 `json:"structured_request_nodes,omitempty"`
	RequestNodes               []Node                 `json:"request_nodes,omitempty"`
	ChatHistory                []ChatHistoryEntry     `json:"chat_history,omitempty"`
	ToolDefinitions            []ToolDefinition       `json:"tool_definitions,omitempty"`
	ToolDefinitionsAlt         []ToolDefinition       `json:"toolDefinitions,omitempty"`
	Tools                      []ToolDefinition       `json:"tools,omitempty"`
	UserGuidelines             string                 `json:"user_guidelines,omitempty"`
	WorkspaceGuidelines        string                 `json:"workspace_guidelines,omitempty"`
	AgentMemories              string                 `json:"agent_memories,omitempty"`
	Mode                       string                 `json:"mode,omitempty"`
	Context                    *ContextBlock          `json:"context,omitempty"`
	Prefix                     string                 `json:"prefix,omitempty"`
	SelectedCode               string                 `json:"selected_code,omitempty"`
	DisableSelectedCodeDetails bool                   `json:"disable_selected_code_details,omitempty"`
	Suffix                     string                 `json:"suffix,omitempty"`
	Diff                       string                 `json:"diff,omitempty"`
	Lang                       string                 `json:"lang,omitempty"`
	Path                       string                 `json:"path,omitempty"`
	Images                     []string               `json:"images,omitempty"`
	Thinking                   interface{}            `json:"thinking,omitempty"`
	EnableThinking             bool                   `json:"enable_thinking,omitempty"`
	MaxTokens                  int                    `json:"max_tokens,omitempty"`
	Stream                     *bool                  `json:"stream,omitempty"`
	Rules                      interface{}            `json:"rules,omitempty"`
	FeatureDetectionFlags      map[string]interface{} `json:"feature_detection_flags,omitempty"`
	PersonaType                int                    `json:"persona_type,omitempty"`
	Silent                     bool                   `json:"silent,omitempty"`
	ByokSystemPrompt           string                 `json:"byok_system_prompt,omitempty"`
}

type ContextBlock struct{ Path, Lang, Prefix, SelectedCode, Suffix, Diff string }
type Node struct {
	Type           int                 `json:"type"`
	TextNode       *TextNode           `json:"text_node,omitempty"`
	ToolResultNode *ToolResultNode     `json:"tool_result_node,omitempty"`
	ImageNode      *ImageNode          `json:"image_node,omitempty"`
	ImageIDNode    *ImageIDNode        `json:"image_id_node,omitempty"`
	IdeStateNode   *IdeStateNode       `json:"ide_state_node,omitempty"`
	EditEventsNode *EditEventsNode     `json:"edit_events_node,omitempty"`
	CheckpointRef  *CheckpointRefNode  `json:"checkpoint_ref_node,omitempty"`
	Personality    *ChangePersonality  `json:"change_personality_node,omitempty"`
	FileNode       *FileNode           `json:"file_node,omitempty"`
	FileIDNode     *FileIDNode         `json:"file_id_node,omitempty"`
	ToolUse        *ToolUseNode        `json:"tool_use,omitempty"`
	Thinking       *ThinkingNode       `json:"thinking,omitempty"`
	HistorySummary *HistorySummaryNode `json:"history_summary,omitempty"`
}
type TextNode struct {
	Content string `json:"content,omitempty"`
	Text    string `json:"text,omitempty"`
}
type ToolResultNode struct {
	ToolUseID    string                  `json:"tool_use_id,omitempty"`
	ToolCallID   string                  `json:"tool_call_id,omitempty"`
	Content      string                  `json:"content,omitempty"`
	ToolResult   string                  `json:"tool_result,omitempty"`
	ContentNodes []ToolResultContentNode `json:"content_nodes,omitempty"`
	IsError      bool                    `json:"is_error,omitempty"`
}
type ToolResultContentNode struct {
	Type         string                  `json:"type,omitempty"`
	NodeType     int                     `json:"node_type,omitempty"`
	Text         string                  `json:"text,omitempty"`
	TextContent  string                  `json:"text_content,omitempty"`
	MediaType    string                  `json:"media_type,omitempty"`
	Data         string                  `json:"data,omitempty"`
	ImageContent *ToolResultImageContent `json:"image_content,omitempty"`
}
type ToolResultImageContent struct {
	ImageData string `json:"image_data,omitempty"`
	Format    int    `json:"format,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}
type ImageNode struct {
	ImageData string `json:"image_data,omitempty"`
	Format    int    `json:"format,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
}
type ImageIDNode struct {
	ImageID string `json:"image_id,omitempty"`
	Format  int    `json:"format,omitempty"`
}
type IdeStateNode struct {
	WorkspaceFolders          []WorkspaceFolder `json:"workspace_folders,omitempty"`
	WorkspaceFoldersUnchanged *bool             `json:"workspace_folders_unchanged,omitempty"`
	CurrentTerminal           *TerminalState    `json:"current_terminal,omitempty"`
}
type WorkspaceFolder struct {
	FolderRoot     string `json:"folder_root,omitempty"`
	RepositoryRoot string `json:"repository_root,omitempty"`
}
type TerminalState struct {
	TerminalID              int    `json:"terminal_id,omitempty"`
	CurrentWorkingDirectory string `json:"current_working_directory,omitempty"`
}
type EditEventsNode struct {
	Source     string          `json:"source,omitempty"`
	EditEvents []FileEditEvent `json:"edit_events,omitempty"`
}
type FileEditEvent struct {
	Path           string         `json:"path,omitempty"`
	BeforeBlobName string         `json:"before_blob_name,omitempty"`
	AfterBlobName  string         `json:"after_blob_name,omitempty"`
	Edits          []TextEditDiff `json:"edits,omitempty"`
}
type TextEditDiff struct {
	AfterLineStart  int    `json:"after_line_start,omitempty"`
	BeforeLineStart int    `json:"before_line_start,omitempty"`
	BeforeText      string `json:"before_text,omitempty"`
	AfterText       string `json:"after_text,omitempty"`
}
type CheckpointRefNode struct {
	RequestID     string `json:"request_id,omitempty"`
	FromTimestamp int64  `json:"from_timestamp,omitempty"`
	ToTimestamp   int64  `json:"to_timestamp,omitempty"`
	Source        string `json:"source,omitempty"`
}
type ChangePersonality struct {
	PersonalityType    int    `json:"personality_type,omitempty"`
	CustomInstructions string `json:"custom_instructions,omitempty"`
}
type FileNode struct {
	FileData string `json:"file_data,omitempty"`
	Format   string `json:"format,omitempty"`
}
type FileIDNode struct {
	FileID   string `json:"file_id,omitempty"`
	FileName string `json:"file_name,omitempty"`
}
type ToolUseNode struct {
	ToolName      string `json:"tool_name"`
	ToolUseID     string `json:"tool_use_id"`
	InputJSON     string `json:"input_json"`
	McpServerName string `json:"mcp_server_name,omitempty"`
	McpToolName   string `json:"mcp_tool_name,omitempty"`
}
type ThinkingNode struct {
	Summary          string `json:"summary,omitempty"`
	Signature        string `json:"signature,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}
type HistorySummaryNode struct {
	SummaryText                         string                   `json:"summary_text,omitempty"`
	SummarizationRequestID              string                   `json:"summarization_request_id,omitempty"`
	HistoryBeginningDroppedNumExchanges int                      `json:"history_beginning_dropped_num_exchanges,omitempty"`
	HistoryMiddleAbridgedText           string                   `json:"history_middle_abridged_text,omitempty"`
	MessageTemplate                     string                   `json:"message_template,omitempty"`
	EndPartFullMaxChars                 int                      `json:"end_part_full_max_chars,omitempty"`
	EndPartFullTailChars                int                      `json:"end_part_full_tail_chars,omitempty"`
	HistoryEnd                          []map[string]interface{} `json:"history_end,omitempty"`
}
type ChatHistoryEntry struct {
	RequestMessage         string `json:"request_message,omitempty"`
	RequestNodes           []Node `json:"request_nodes,omitempty"`
	StructuredRequestNodes []Node `json:"structured_request_nodes,omitempty"`
	Nodes                  []Node `json:"nodes,omitempty"`
	ResponseText           string `json:"response_text,omitempty"`
	ResponseNodes          []Node `json:"response_nodes,omitempty"`
	StructuredOutputNodes  []Node `json:"structured_output_nodes,omitempty"`
}

func (e ChatHistoryEntry) EffectiveRequestNodes() []Node {
	return mergeNodes(e.RequestNodes, e.StructuredRequestNodes, e.Nodes)
}
func (e ChatHistoryEntry) EffectiveResponseNodes() []Node {
	return mergeNodes(e.ResponseNodes, e.StructuredOutputNodes)
}

type ToolDefinition struct {
	Name               string                 `json:"name"`
	Description        string                 `json:"description,omitempty"`
	InputSchema        map[string]interface{} `json:"input_schema,omitempty"`
	InputSchemaAlt     map[string]interface{} `json:"inputSchema,omitempty"`
	InputSchemaJSON    string                 `json:"input_schema_json,omitempty"`
	InputSchemaJSONAlt string                 `json:"inputSchemaJson,omitempty"`
	Parameters         map[string]interface{} `json:"parameters,omitempty"`
	McpServerName      string                 `json:"mcp_server_name,omitempty"`
	McpToolName        string                 `json:"mcp_tool_name,omitempty"`
}
type encryptedBody struct {
	EncryptedData string `json:"encrypted_data"`
	IV            string `json:"iv"`
}

func NormalizeChatStreamRequest(raw []byte) ([]byte, bool, error) {
	prepared, req, err := prepareChatStreamRequest(raw)
	if err != nil {
		return nil, true, err
	}
	_ = prepared
	out, err := buildAnthropicRequest(req)
	if err != nil {
		return nil, true, err
	}
	normalized, err := json.Marshal(out)
	if err != nil {
		return nil, true, err
	}
	return normalized, req.IsStreaming(), nil
}
func prepareChatStreamRequest(raw []byte) ([]byte, *Request, error) {
	prepared, err := preprocessRequestBody(raw)
	if err != nil {
		return nil, nil, err
	}
	var req Request
	if err := json.Unmarshal(prepared, &req); err != nil {
		return nil, nil, err
	}
	return prepared, &req, nil
}
func preprocessRequestBody(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	prepared := raw
	if isEncrypted(raw) {
		decrypted, err := decryptRequestBody(raw)
		if err != nil {
			return nil, err
		}
		prepared = decrypted
	}
	normalized := prepared
	if structured, err := normalizeStructuredRequestBody(prepared); err == nil {
		normalized = structured
	}
	if req, err := parseStructuredRequest(normalized); err == nil && requestLooksStructured(req) {
		return normalized, nil
	}
	return reconstructFromPlaintext(prepared)
}
func parseStructuredRequest(raw []byte) (*Request, error) {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	return &req, nil
}
func normalizeStructuredRequestBody(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var payload interface{}
	if err := dec.Decode(&payload); err != nil {
		return nil, err
	}
	normalized := normalizeAugmentJSONValue(payload)
	if root, ok := normalized.(map[string]interface{}); ok {
		normalizeAugmentRootMap(root)
	}
	return json.Marshal(normalized)
}
func normalizeAugmentRootMap(root map[string]interface{}) {
	if root == nil {
		return
	}
	if strings.TrimSpace(stringValue(root["agent_memories"])) == "" {
		if text := extractAugmentAgentMemories(root); text != "" {
			root["agent_memories"] = text
		}
	}
}
func extractAugmentAgentMemories(root map[string]interface{}) string {
	info, ok := root["memories_info"].(map[string]interface{})
	if !ok {
		return ""
	}
	for _, key := range []string{"agent_memories", "memories", "memory", "text", "content"} {
		if text := strings.TrimSpace(stringValue(info[key])); text != "" {
			return text
		}
	}
	items, ok := info["items"].([]interface{})
	if !ok || len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if text := strings.TrimSpace(stringValue(item)); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}
func normalizeAugmentJSONValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		normalized := make(map[string]interface{}, len(typed))
		for key, raw := range typed {
			canonical := normalizeAugmentJSONKey(key)
			candidate := normalizeAugmentJSONValue(raw)
			if existing, exists := normalized[canonical]; exists {
				normalized[canonical] = mergeNormalizedAugmentJSONValue(existing, candidate)
				continue
			}
			normalized[canonical] = candidate
		}
		return normalized
	case []interface{}:
		normalized := make([]interface{}, len(typed))
		for i, item := range typed {
			normalized[i] = normalizeAugmentJSONValue(item)
		}
		return normalized
	default:
		return value
	}
}
func normalizeAugmentJSONKey(key string) string {
	normalized := camelToSnakeKey(strings.TrimSpace(key))
	switch normalized {
	case "prompt", "instruction":
		return "message"
	case "language":
		return "lang"
	case "selected_text", "selected_code_snippet":
		return "selected_code"
	case "byok_system":
		return "byok_system_prompt"
	case "history_summary_node":
		return "history_summary"
	case "max_output_tokens":
		return "max_tokens"
	default:
		return normalized
	}
}
func mergeNormalizedAugmentJSONValue(existing, incoming interface{}) interface{} {
	if isZeroAugmentJSONValue(existing) {
		return incoming
	}
	return existing
}
func isZeroAugmentJSONValue(value interface{}) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case bool:
		return !typed
	case []interface{}:
		return len(typed) == 0
	case map[string]interface{}:
		return len(typed) == 0
	case json.Number:
		return typed.String() == "0"
	case float64:
		return typed == 0
	case int:
		return typed == 0
	default:
		return false
	}
}
func camelToSnakeKey(key string) string {
	if key == "" {
		return ""
	}
	runes := []rune(key)
	var b strings.Builder
	for i, r := range runes {
		switch {
		case r == '-':
			b.WriteByte('_')
		case unicode.IsUpper(r):
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if prev != '_' && prev != '-' && (unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower)) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
func requestLooksStructured(req *Request) bool {
	if req == nil {
		return false
	}
	if strings.TrimSpace(req.Message) != "" {
		return true
	}
	if len(req.EffectiveCurrentNodes()) > 0 || len(req.ChatHistory) > 0 || len(req.EffectiveTools()) > 0 {
		return true
	}
	return req.EffectiveContext() != nil
}
func isEncrypted(raw []byte) bool {
	var body encryptedBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return false
	}
	return strings.TrimSpace(body.EncryptedData) != "" && strings.TrimSpace(body.IV) != ""
}
func decryptRequestBody(raw []byte) ([]byte, error) {
	privateKey, err := parseEmbeddedPrivateKey()
	if err != nil {
		return nil, err
	}
	var body encryptedBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("augment decryptor: unmarshal body: %w", err)
	}
	encryptedData := strings.TrimSpace(body.EncryptedData)
	if len(encryptedData) > maxAugmentEncryptedFieldBytes {
		return nil, fmt.Errorf("augment decryptor: encrypted payload exceeds limit")
	}
	ivValue := strings.TrimSpace(body.IV)
	if len(ivValue) > 4096 {
		return nil, fmt.Errorf("augment decryptor: encrypted key envelope exceeds limit")
	}
	ivBytes, err := base64.StdEncoding.DecodeString(ivValue)
	if err != nil {
		return nil, fmt.Errorf("augment decryptor: decode IV: %w", err)
	}
	decryptedKeyIV, err := rsa.DecryptOAEP(sha256.New(), nil, privateKey, ivBytes, nil)
	if err != nil {
		return nil, fmt.Errorf("augment decryptor: RSA decrypt: %w", err)
	}
	aesKey, aesIV, err := parseAESKeyIV(decryptedKeyIV)
	if err != nil {
		return nil, fmt.Errorf("augment decryptor: parse AES key/IV: %w", err)
	}
	ciphertext, err := decodeHexOrBase64(encryptedData)
	if err != nil {
		return nil, fmt.Errorf("augment decryptor: decode ciphertext: %w", err)
	}
	if len(ciphertext) > maxAugmentDecryptedBodyBytes+aes.BlockSize {
		return nil, fmt.Errorf("augment decryptor: encrypted payload exceeds limit")
	}
	plaintext, err := aesCBCDecrypt(aesKey, aesIV, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("augment decryptor: AES decrypt: %w", err)
	}
	if len(plaintext) > maxAugmentDecryptedBodyBytes {
		return nil, fmt.Errorf("augment decryptor: decrypted body exceeds limit")
	}
	return plaintext, nil
}
func parseEmbeddedPrivateKey() (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(embeddedAugmentPrivateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("augment decryptor: invalid embedded PEM data")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("augment decryptor: not an RSA private key")
	}
	return rsaKey, nil
}
func reconstructFromPlaintext(raw []byte) ([]byte, error) {
	var body struct {
		Model  string   `json:"model"`
		Data   string   `json:"data"`
		Images []string `json:"images"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if strings.TrimSpace(body.Data) == "" {
		return nil, fmt.Errorf("invalid request format")
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = "claude-sonnet-4-5-20250929"
	}
	streamTrue := true
	return json.Marshal(map[string]any{"model": model, "message": body.Data, "images": body.Images, "max_tokens": 4096, "stream": streamTrue})
}
func parseAESKeyIV(raw []byte) ([]byte, []byte, error) {
	if len(raw) == 48 {
		return raw[:32], raw[32:48], nil
	}
	parts := strings.SplitN(string(raw), "::", 2)
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("unexpected decrypted blob")
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(parts[0]))
	key := hasher.Sum(nil)
	iv, err := decodeHexBytes(parts[1])
	if err != nil {
		return nil, nil, err
	}
	return key, iv, nil
}
func aesCBCDecrypt(key, iv, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)
	return pkcs7Unpad(plaintext)
}
func pkcs7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty data")
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > aes.BlockSize || padLen > len(data) {
		return nil, fmt.Errorf("invalid padding")
	}
	for i := len(data) - padLen; i < len(data); i++ {
		if data[i] != byte(padLen) {
			return nil, fmt.Errorf("invalid padding")
		}
	}
	return data[:len(data)-padLen], nil
}
func decodeHexOrBase64(s string) ([]byte, error) {
	if out, err := decodeHexBytes(s); err == nil {
		return out, nil
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(s))
}
func decodeHexBytes(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("odd hex")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		hi := hexVal(s[i])
		lo := hexVal(s[i+1])
		if hi < 0 || lo < 0 {
			return nil, fmt.Errorf("invalid hex")
		}
		out[i/2] = byte(hi<<4 | lo)
	}
	return out, nil
}
func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}
func buildAnthropicRequest(req *Request) (*apicompat.AnthropicRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("augment request is nil")
	}
	messages, err := buildMessages(req)
	if err != nil {
		return nil, err
	}
	messages = repairAnthropicToolUsePairs(messages)
	if len(messages) == 0 {
		messages = []apicompat.AnthropicMessage{emptyAnthropicUserMessage()}
	} else if messages[0].Role != "user" {
		messages = append([]apicompat.AnthropicMessage{emptyAnthropicUserMessage()}, messages...)
	}
	body := &apicompat.AnthropicRequest{
		Model:     strings.TrimSpace(req.Model),
		MaxTokens: effectiveMaxTokens(req.MaxTokens),
		Messages:  messages,
		Stream:    req.IsStreaming(),
	}
	if tools := buildTools(req.EffectiveTools()); len(tools) > 0 && !req.Silent {
		body.Tools = tools
		body.ToolChoice = mustMarshal(map[string]any{"type": "auto"})
	}
	if system := buildSystem(req); len(system) > 0 {
		body.System = mustMarshal(system)
	}
	if thinking := buildThinking(req); thinking != nil {
		body.Thinking = thinking
	}
	return body, nil
}
func buildMessages(req *Request) ([]apicompat.AnthropicMessage, error) {
	messages := make([]apicompat.AnthropicMessage, 0)
	history, currentNodes := preprocessHistoryForAPI(req)
	for _, entry := range history {
		if msg := buildUserMessage(entry.RequestMessage, entry.EffectiveRequestNodes(), nil, nil, false); msg != nil {
			messages = append(messages, *msg)
		}
		if msg := buildAssistantMessage(entry.ResponseText, entry.EffectiveResponseNodes()); msg != nil {
			messages = append(messages, *msg)
		}
	}
	if msg := buildUserMessage(req.Message, currentNodes, req.EffectiveContext(), req.Images, false); msg != nil {
		messages = append(messages, *msg)
	}
	return messages, nil
}
func buildUserMessage(message string, nodes []Node, ctx *ContextBlock, topImages []string, includeContext bool) *apicompat.AnthropicMessage {
	blocks := make([]apicompat.AnthropicContentBlock, 0)
	textParts := make([]string, 0)
	if trimmed := strings.TrimSpace(message); trimmed != "" {
		textParts = append(textParts, trimmed)
	}
	for _, node := range nodes {
		if node.Type == 1 && node.ToolResultNode != nil {
			blocks = append(blocks, apicompat.AnthropicContentBlock{Type: "tool_result", ToolUseID: effectiveToolUseID(node.ToolResultNode), Content: buildToolResultContent(node.ToolResultNode), IsError: node.ToolResultNode.IsError})
			continue
		}
		if text := strings.TrimSpace(formatPromptNode(node)); text != "" {
			textParts = append(textParts, text)
			continue
		}
		if block := imageBlockForNode(node); block != nil {
			blocks = append(blocks, *block)
		}
	}
	if includeContext {
		if contextText := buildContextText(ctx); contextText != "" {
			textParts = append(textParts, contextText)
		}
	}
	for _, raw := range topImages {
		if block := buildRawImageBlock(raw, defaultImageMediaType); block != nil {
			blocks = append(blocks, *block)
		}
	}
	if len(textParts) > 0 {
		blocks = append([]apicompat.AnthropicContentBlock{{Type: "text", Text: strings.Join(textParts, "\n\n")}}, blocks...)
	}
	if len(blocks) == 0 {
		return nil
	}
	return &apicompat.AnthropicMessage{Role: "user", Content: mustMarshal(blocks)}
}
func buildAssistantMessage(text string, nodes []Node) *apicompat.AnthropicMessage {
	blocks := make([]apicompat.AnthropicContentBlock, 0)
	preferCompletedToolUse := false
	hasText := false
	for _, node := range nodes {
		if node.Type == 5 && node.ToolUse != nil {
			preferCompletedToolUse = true
			break
		}
	}
	for _, node := range nodes {
		switch node.Type {
		case 0:
			if text := strings.TrimSpace(nodeText(node.TextNode)); text != "" {
				blocks = append(blocks, apicompat.AnthropicContentBlock{Type: "text", Text: text})
				hasText = true
			}
		case 2:
			if text := strings.TrimSpace(nodeText(node.TextNode)); text != "" {
				blocks = append(blocks, apicompat.AnthropicContentBlock{Type: "text", Text: text})
				hasText = true
				continue
			}
			if block := buildImageBlock(node.ImageNode); block != nil {
				blocks = append(blocks, *block)
			}
		case 3, 5, 7:
			if node.ToolUse != nil {
				if node.Type == 7 && preferCompletedToolUse {
					continue
				}
				blocks = append(blocks, apicompat.AnthropicContentBlock{
					Type:  "tool_use",
					ID:    strings.TrimSpace(node.ToolUse.ToolUseID),
					Name:  strings.TrimSpace(node.ToolUse.ToolName),
					Input: normalizeJSONObject(node.ToolUse.InputJSON),
				})
			}
		case 8:
			if node.Thinking != nil {
				text := strings.TrimSpace(node.Thinking.Summary)
				if text == "" {
					text = strings.TrimSpace(node.Thinking.EncryptedContent)
				}
				if text != "" {
					blocks = append(blocks, apicompat.AnthropicContentBlock{Type: "thinking", Thinking: text})
				}
			}
		}
	}

	if trimmed := strings.TrimSpace(text); trimmed != "" && !hasText {
		blocks = append(blocks, apicompat.AnthropicContentBlock{Type: "text", Text: trimmed})
		hasText = true
	}
	if len(blocks) == 0 {
		return nil
	}
	if !hasText {
		blocks = append([]apicompat.AnthropicContentBlock{{Type: "text", Text: ""}}, blocks...)
	}
	return &apicompat.AnthropicMessage{Role: "assistant", Content: mustMarshal(blocks)}
}
func buildTools(defs []ToolDefinition) []apicompat.AnthropicTool {
	tools := make([]apicompat.AnthropicTool, 0, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		tools = append(tools, apicompat.AnthropicTool{Name: name, Description: def.Description, InputSchema: mustMarshal(def.EffectiveInputSchema())})
	}
	if len(tools) == 0 {
		return nil
	}
	return tools
}
func buildSystem(req *Request) []apicompat.AnthropicContentBlock {
	parts := make([]string, 0)
	if persona := personaTypeToLabel(req.PersonaType); persona != "" && persona != "DEFAULT" {
		parts = append(parts, "Persona: "+persona)
	}
	for _, text := range []string{strings.TrimSpace(req.UserGuidelines), strings.TrimSpace(req.WorkspaceGuidelines), strings.TrimSpace(coerceRulesText(req.Rules)), strings.TrimSpace(req.AgentMemories), strings.TrimSpace(req.ByokSystemPrompt)} {
		if text != "" {
			parts = append(parts, text)
		}
	}
	if strings.EqualFold(strings.TrimSpace(req.Mode), "agent") {
		parts = append(parts, "You are an AI coding assistant with access to tools. Use tools when needed to complete tasks.")
	}
	if contextText := buildSystemContextText(req.EffectiveContext(), !req.DisableSelectedCodeDetails); contextText != "" {
		parts = append(parts, contextText)
	}
	if len(parts) == 0 {
		return nil
	}
	return []apicompat.AnthropicContentBlock{{Type: "text", Text: strings.Join(parts, "\n\n")}}
}
func buildThinking(req *Request) *apicompat.AnthropicThinking {
	if thinkingMap, ok := req.Thinking.(map[string]any); ok {
		if typ := strings.TrimSpace(stringValue(thinkingMap["type"])); typ != "" {
			return &apicompat.AnthropicThinking{Type: typ, BudgetTokens: intValue(thinkingMap["budget_tokens"])}
		}
	}
	if req.EnableThinking {
		budget := effectiveMaxTokens(req.MaxTokens) - 1
		if budget < 1 {
			budget = 1
		}
		return &apicompat.AnthropicThinking{Type: "enabled", BudgetTokens: budget}
	}
	return nil
}
func buildContextText(ctx *ContextBlock) string {
	if ctx == nil {
		return ""
	}
	parts := make([]string, 0)
	if text := strings.TrimSpace(ctx.Path); text != "" {
		parts = append(parts, "[path]\n"+text)
	}
	if text := strings.TrimSpace(ctx.Lang); text != "" {
		parts = append(parts, "[lang]\n"+text)
	}
	if text := strings.TrimSpace(ctx.Prefix); text != "" {
		parts = append(parts, "[prefix]\n"+text)
	}
	if text := strings.TrimSpace(ctx.SelectedCode); text != "" {
		parts = append(parts, "[selected_code]\n"+text)
	}
	if text := strings.TrimSpace(ctx.Suffix); text != "" {
		parts = append(parts, "[suffix]\n"+text)
	}
	if text := strings.TrimSpace(ctx.Diff); text != "" {
		parts = append(parts, "[diff]\n"+text)
	}
	return strings.Join(parts, "\n\n")
}
func buildSystemContextText(ctx *ContextBlock, includeSelectedDetails bool) string {
	if ctx == nil {
		return ""
	}
	parts := make([]string, 0)
	if text := strings.TrimSpace(ctx.Path); text != "" {
		parts = append(parts, "[path]\n"+text)
	}
	if text := strings.TrimSpace(ctx.Lang); text != "" {
		parts = append(parts, "[lang]\n"+text)
	}
	if includeSelectedDetails {
		if text := strings.TrimSpace(ctx.Prefix); text != "" {
			parts = append(parts, "[prefix]\n"+text)
		}
		if text := strings.TrimSpace(ctx.SelectedCode); text != "" {
			parts = append(parts, "[selected_code]\n"+text)
		}
		if text := strings.TrimSpace(ctx.Suffix); text != "" {
			parts = append(parts, "[suffix]\n"+text)
		}
		if text := strings.TrimSpace(ctx.Diff); text != "" {
			parts = append(parts, "[diff]\n"+text)
		}
	}
	return strings.Join(parts, "\n\n")
}
func preprocessHistoryForAPI(req *Request) ([]ChatHistoryEntry, []Node) {
	if req == nil {
		return nil, nil
	}

	history := append([]ChatHistoryEntry(nil), req.ChatHistory...)
	currentNodes := req.EffectiveCurrentNodes()

	combined := append([]ChatHistoryEntry{}, history...)
	combined = append(combined, ChatHistoryEntry{RequestNodes: currentNodes})

	start := -1
	for i := len(combined) - 1; i >= 0; i-- {
		if chatHistoryEntryHasSummary(combined[i]) {
			start = i
			break
		}
	}
	if start == -1 {
		return history, currentNodes
	}

	processed := append([]ChatHistoryEntry(nil), combined[start:]...)
	if len(processed) == 0 {
		return history, currentNodes
	}
	processed[0] = compactHistorySummaryEntry(processed[0])

	last := processed[len(processed)-1]
	processedCurrentNodes := last.EffectiveRequestNodes()
	if len(processedCurrentNodes) == 0 {
		processedCurrentNodes = currentNodes
	}

	return processed[:len(processed)-1], processedCurrentNodes
}

func chatHistoryEntryHasSummary(entry ChatHistoryEntry) bool {
	return findHistorySummaryNode(entry.EffectiveRequestNodes()) != nil
}

func findHistorySummaryNode(nodes []Node) *Node {
	for i := range nodes {
		if nodes[i].Type == 10 && nodes[i].HistorySummary != nil {
			return &nodes[i]
		}
	}
	return nil
}

func compactHistorySummaryEntry(entry ChatHistoryEntry) ChatHistoryEntry {
	nodes := entry.EffectiveRequestNodes()
	summaryNode := findHistorySummaryNode(nodes)
	if summaryNode == nil || summaryNode.HistorySummary == nil {
		return entry
	}

	otherNodes := make([]Node, 0, len(nodes))
	extraToolResults := make([]*ToolResultNode, 0)
	for _, node := range nodes {
		switch {
		case node.Type == 10 && node.HistorySummary != nil:
			continue
		case node.Type == 1 && node.ToolResultNode != nil:
			extraToolResults = append(extraToolResults, node.ToolResultNode)
		default:
			otherNodes = append(otherNodes, node)
		}
	}

	if text := strings.TrimSpace(renderHistorySummaryNodeValue(summaryNode.HistorySummary, extraToolResults)); text != "" {
		otherNodes = append([]Node{{Type: 0, TextNode: &TextNode{Content: text}}}, otherNodes...)
	}

	entry.RequestNodes = otherNodes
	entry.StructuredRequestNodes = nil
	entry.Nodes = nil
	return entry
}

func renderHistorySummaryNodeValue(node *HistorySummaryNode, extraToolResults []*ToolResultNode) string {
	if node == nil {
		return ""
	}

	template := strings.TrimSpace(node.MessageTemplate)
	if template == "" {
		return strings.TrimSpace(node.SummaryText)
	}

	historyEnd := normalizeHistorySummaryEnd(node)
	if len(extraToolResults) > 0 {
		historyEnd = append(historyEnd, ChatHistoryEntry{
			RequestNodes: buildHistorySummaryToolResultNodes(extraToolResults),
		})
	}

	endPartFull := renderHistorySummaryEnd(historyEnd)
	maxChars := node.EndPartFullMaxChars
	tailChars := node.EndPartFullTailChars
	if maxChars > 0 && len(endPartFull) > maxChars {
		if tailChars <= 0 || tailChars >= maxChars {
			tailChars = minInt(2048, maxChars/2)
		}
		headChars := maxChars - tailChars - 5
		if headChars < 0 {
			headChars = 0
		}
		endPartFull = endPartFull[:headChars] + "\n...\n" + endPartFull[len(endPartFull)-tailChars:]
	}

	replacer := strings.NewReplacer(
		"{summary}", node.SummaryText,
		"{summarization_request_id}", node.SummarizationRequestID,
		"{beginning_part_dropped_num_exchanges}", fmt.Sprintf("%d", node.HistoryBeginningDroppedNumExchanges),
		"{middle_part_abridged}", node.HistoryMiddleAbridgedText,
		"{end_part_full}", endPartFull,
	)
	return strings.TrimSpace(replacer.Replace(template))
}

func normalizeHistorySummaryEnd(node *HistorySummaryNode) []ChatHistoryEntry {
	if node == nil || len(node.HistoryEnd) == 0 {
		return nil
	}

	out := make([]ChatHistoryEntry, 0, len(node.HistoryEnd))
	for _, item := range node.HistoryEnd {
		out = append(out, ChatHistoryEntry{
			RequestMessage: firstMapString(item, "request_message"),
			ResponseText:   firstMapString(item, "response_text"),
			RequestNodes:   decodeNodesFromInterface(item["request_nodes"]),
			ResponseNodes:  decodeNodesFromInterface(item["response_nodes"]),
		})
	}
	return out
}

func buildHistorySummaryToolResultNodes(results []*ToolResultNode) []Node {
	if len(results) == 0 {
		return nil
	}
	nodes := make([]Node, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue
		}
		nodes = append(nodes, Node{Type: 1, ToolResultNode: result})
	}
	return nodes
}

func renderHistorySummaryEnd(entries []ChatHistoryEntry) string {
	if len(entries) == 0 {
		return ""
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, renderHistorySummaryExchange(entry))
	}
	return strings.Join(parts, "\n")
}

func renderHistorySummaryExchange(entry ChatHistoryEntry) string {
	lines := []string{"<exchange>", "  <user_request_or_tool_results>"}
	if text := strings.TrimSpace(entry.RequestMessage); text != "" {
		lines = append(lines, text)
	}
	for _, node := range entry.EffectiveRequestNodes() {
		switch {
		case node.Type == 1 && node.ToolResultNode != nil:
			lines = append(lines,
				fmt.Sprintf("    <tool_result tool_use_id=\"%s\" is_error=\"%t\">", effectiveToolUseID(node.ToolResultNode), node.ToolResultNode.IsError),
				toolResultContentText(buildToolResultContent(node.ToolResultNode)),
				"    </tool_result>",
			)
		default:
			if text := strings.TrimSpace(formatPromptNode(node)); text != "" {
				lines = append(lines, text)
			}
		}
	}
	lines = append(lines, "  </user_request_or_tool_results>")

	thinkingParts := make([]string, 0)
	responseParts := make([]string, 0)
	if text := strings.TrimSpace(entry.ResponseText); text != "" {
		responseParts = append(responseParts, text)
	}
	for _, node := range entry.EffectiveResponseNodes() {
		switch node.Type {
		case 8:
			if node.Thinking != nil {
				if text := strings.TrimSpace(node.Thinking.Summary); text != "" {
					thinkingParts = append(thinkingParts, text)
				}
			}
		case 5:
			if node.ToolUse != nil {
				responseParts = append(responseParts,
					fmt.Sprintf("<tool_use name=\"%s\" tool_use_id=\"%s\">", strings.TrimSpace(node.ToolUse.ToolName), strings.TrimSpace(node.ToolUse.ToolUseID)),
					strings.TrimSpace(node.ToolUse.InputJSON),
					"</tool_use>",
				)
			}
		default:
			if text := strings.TrimSpace(formatPromptNode(node)); text != "" {
				responseParts = append(responseParts, text)
			}
		}
	}
	if len(thinkingParts) > 0 {
		lines = append(lines, "  <assistant_thinking>")
		for _, part := range thinkingParts {
			lines = append(lines, "    <thinking>", part, "    </thinking>")
		}
		lines = append(lines, "  </assistant_thinking>")
	}
	if len(responseParts) > 0 {
		lines = append(lines, "  <assistant_response>")
		lines = append(lines, responseParts...)
		lines = append(lines, "  </assistant_response>")
	}
	lines = append(lines, "</exchange>")
	return strings.Join(lines, "\n")
}
func formatPromptNode(node Node) string {
	switch node.Type {
	case 0, 2:
		return nodeText(node.TextNode)
	case 3:
		return formatImageIDPrompt(node.ImageIDNode)
	case 4:
		return formatIdeStatePrompt(node.IdeStateNode)
	case 5:
		return formatEditEventsPrompt(node.EditEventsNode)
	case 6:
		return formatCheckpointRefPrompt(node.CheckpointRef)
	case 7:
		return formatChangePersonalityPrompt(node.Personality)
	case 8:
		return formatFileNodePrompt(node.FileNode)
	case 9:
		return formatFileIDPrompt(node.FileIDNode)
	case 10:
		return formatHistorySummaryPrompt(node.HistorySummary)
	default:
		return ""
	}
}
func imageBlockForNode(node Node) *apicompat.AnthropicContentBlock {
	if node.Type != 2 {
		return nil
	}
	return buildImageBlock(node.ImageNode)
}
func formatIdeStatePrompt(node *IdeStateNode) string {
	if node == nil {
		return ""
	}
	lines := []string{"[IDE_STATE]"}
	if len(node.WorkspaceFolders) > 0 {
		lines = append(lines, "workspace_folders:")
	}
	for _, folder := range node.WorkspaceFolders {
		lines = append(lines, fmt.Sprintf("- repository_root=%s folder_root=%s", emptyFallback(folder.RepositoryRoot), emptyFallback(folder.FolderRoot)))
	}
	if node.CurrentTerminal != nil {
		lines = append(lines, fmt.Sprintf("current_terminal: id=%d cwd=%s", node.CurrentTerminal.TerminalID, emptyFallback(node.CurrentTerminal.CurrentWorkingDirectory)))
	}
	if len(lines) == 1 {
		return ""
	}
	lines = append(lines, "[/IDE_STATE]")
	return strings.Join(lines, "\n")
}
func formatEditEventsPrompt(node *EditEventsNode) string {
	if node == nil {
		return ""
	}
	lines := []string{"[EDIT_EVENTS]"}
	for _, event := range node.EditEvents {
		lines = append(lines, fmt.Sprintf("- file: %s edits=%d", emptyFallback(event.Path), len(event.Edits)))
	}
	if len(lines) == 1 {
		return ""
	}
	lines = append(lines, "[/EDIT_EVENTS]")
	return strings.Join(lines, "\n")
}
func formatCheckpointRefPrompt(node *CheckpointRefNode) string {
	if node == nil {
		return ""
	}
	lines := []string{"[CHECKPOINT_REF]"}
	if text := strings.TrimSpace(node.RequestID); text != "" {
		lines = append(lines, "request_id="+text)
	}
	if len(lines) == 1 {
		return ""
	}
	lines = append(lines, "[/CHECKPOINT_REF]")
	return strings.Join(lines, "\n")
}
func formatChangePersonalityPrompt(node *ChangePersonality) string {
	if node == nil {
		return ""
	}
	lines := []string{"[CHANGE_PERSONALITY]", fmt.Sprintf("personality_type=%d", node.PersonalityType)}
	if text := strings.TrimSpace(node.CustomInstructions); text != "" {
		lines = append(lines, "custom_instructions="+text)
	}
	lines = append(lines, "[/CHANGE_PERSONALITY]")
	return strings.Join(lines, "\n")
}
func formatImageIDPrompt(node *ImageIDNode) string {
	if node == nil || strings.TrimSpace(node.ImageID) == "" {
		return ""
	}
	return fmt.Sprintf("[IMAGE_ID] image_id=%s format=%d", strings.TrimSpace(node.ImageID), node.Format)
}
func formatFileIDPrompt(node *FileIDNode) string {
	if node == nil {
		return ""
	}
	if strings.TrimSpace(node.FileID) == "" && strings.TrimSpace(node.FileName) == "" {
		return ""
	}
	return fmt.Sprintf("[FILE_ID] file_name=%s file_id=%s", emptyFallback(strings.TrimSpace(node.FileName)), emptyFallback(strings.TrimSpace(node.FileID)))
}
func formatFileNodePrompt(node *FileNode) string {
	if node == nil {
		return ""
	}
	format := strings.TrimSpace(node.Format)
	if format == "" {
		format = "application/octet-stream"
	}
	raw := strings.TrimSpace(node.FileData)
	if raw == "" {
		return "[FILE] format=" + format + " (empty)"
	}
	return fmt.Sprintf("[FILE] format=%s bytes≈%d", format, (len(raw)*3)/4)
}
func formatHistorySummaryPrompt(node *HistorySummaryNode) string {
	if node == nil {
		return ""
	}
	if rendered := strings.TrimSpace(renderHistorySummaryNodeValue(node, nil)); rendered != "" {
		return rendered
	}
	if strings.TrimSpace(node.SummaryText) != "" {
		return node.SummaryText
	}
	return ""
}
func buildImageBlock(node *ImageNode) *apicompat.AnthropicContentBlock {
	if node == nil {
		return nil
	}
	raw := strings.TrimSpace(node.Data)
	if raw == "" {
		raw = strings.TrimSpace(node.ImageData)
	}
	mediaType := strings.TrimSpace(node.MediaType)
	if mediaType == "" && node.Format != 0 {
		mediaType = imageFormatMediaTypes[node.Format]
	}
	return buildRawImageBlock(raw, mediaType)
}
func buildToolResultContent(node *ToolResultNode) json.RawMessage {
	if node == nil {
		return mustMarshal("")
	}

	blocks := make([]apicompat.AnthropicContentBlock, 0)
	for _, item := range node.ContentNodes {
		switch item.EffectiveType() {
		case "image":
			mediaType, data := toolResultImage(item)
			if data != "" {
				blocks = append(blocks, apicompat.AnthropicContentBlock{
					Type: "image",
					Source: &apicompat.AnthropicImageSource{
						Type:      "base64",
						MediaType: mediaType,
						Data:      data,
					},
				})
			}
		case "text":
			if text := strings.TrimSpace(item.EffectiveText()); text != "" {
				blocks = append(blocks, apicompat.AnthropicContentBlock{Type: "text", Text: text})
			}
		}
	}

	if len(blocks) > 0 {
		return mustMarshal(blocks)
	}
	return mustMarshal(strings.TrimSpace(nodeEffectiveContent(node)))
}
func toolResultImage(item ToolResultContentNode) (string, string) {
	if item.ImageContent != nil {
		mediaType := strings.TrimSpace(item.ImageContent.MediaType)
		if mediaType == "" {
			mediaType = strings.TrimSpace(item.MediaType)
		}
		if mediaType == "" {
			mediaType = "image/png"
		}
		data := strings.TrimSpace(item.ImageContent.Data)
		if data == "" {
			data = strings.TrimSpace(item.ImageContent.ImageData)
		}
		return mediaType, data
	}
	mediaType := strings.TrimSpace(item.MediaType)
	if mediaType == "" {
		mediaType = "image/png"
	}
	return mediaType, strings.TrimSpace(item.Data)
}
func nodeText(node *TextNode) string {
	if node == nil {
		return ""
	}
	if strings.TrimSpace(node.Content) != "" {
		return node.Content
	}
	return node.Text
}
func nodeEffectiveContent(node *ToolResultNode) string {
	if node == nil {
		return ""
	}
	if strings.TrimSpace(node.Content) != "" {
		return node.Content
	}
	return node.ToolResult
}
func effectiveToolUseID(node *ToolResultNode) string {
	if node == nil {
		return ""
	}
	if strings.TrimSpace(node.ToolUseID) != "" {
		return node.ToolUseID
	}
	return strings.TrimSpace(node.ToolCallID)
}
func (r *Request) EffectiveTools() []ToolDefinition {
	if len(r.ToolDefinitions) > 0 {
		return r.ToolDefinitions
	}
	if len(r.ToolDefinitionsAlt) > 0 {
		return r.ToolDefinitionsAlt
	}
	return r.Tools
}
func (r *Request) EffectiveCurrentNodes() []Node {
	return mergeNodes(r.Nodes, r.StructuredRequestNodes, r.RequestNodes)
}
func (r *Request) EffectiveContext() *ContextBlock {
	var ctx ContextBlock
	if r.Context != nil {
		ctx = *r.Context
	}
	if ctx.Path == "" {
		ctx.Path = r.Path
	}
	if ctx.Lang == "" {
		ctx.Lang = r.Lang
	}
	if ctx.Prefix == "" {
		ctx.Prefix = r.Prefix
	}
	if ctx.SelectedCode == "" {
		ctx.SelectedCode = r.SelectedCode
	}
	if ctx.Suffix == "" {
		ctx.Suffix = r.Suffix
	}
	if ctx.Diff == "" {
		ctx.Diff = r.Diff
	}
	if ctx.Path == "" && ctx.Lang == "" && ctx.Prefix == "" && ctx.SelectedCode == "" && ctx.Suffix == "" && ctx.Diff == "" {
		return nil
	}
	return &ctx
}
func (r *Request) IsStreaming() bool {
	if r.Stream == nil {
		return true
	}
	return *r.Stream
}
func (t *ToolDefinition) EffectiveInputSchema() map[string]interface{} {
	if len(t.InputSchema) > 0 {
		return t.InputSchema
	}
	if len(t.InputSchemaAlt) > 0 {
		return t.InputSchemaAlt
	}
	if s := strings.TrimSpace(t.InputSchemaJSON); s != "" {
		var out map[string]interface{}
		if json.Unmarshal([]byte(s), &out) == nil && len(out) > 0 {
			return out
		}
	}
	if s := strings.TrimSpace(t.InputSchemaJSONAlt); s != "" {
		var out map[string]interface{}
		if json.Unmarshal([]byte(s), &out) == nil && len(out) > 0 {
			return out
		}
	}
	if len(t.Parameters) > 0 {
		return t.Parameters
	}
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (n *ToolResultContentNode) EffectiveType() string {
	if n == nil {
		return ""
	}
	if n.Type != "" {
		return n.Type
	}
	switch n.NodeType {
	case 1:
		return "text"
	case 2:
		return "image"
	}
	if n.ImageContent != nil {
		return "image"
	}
	if n.Text != "" || n.TextContent != "" {
		return "text"
	}
	return ""
}
func (n *ToolResultContentNode) EffectiveText() string {
	if n == nil {
		return ""
	}
	if n.Text != "" {
		return n.Text
	}
	return n.TextContent
}
func mergeNodes(groups ...[]Node) []Node {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	if total == 0 {
		return nil
	}
	out := make([]Node, 0, total)
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}
func effectiveMaxTokens(n int) int {
	if n <= 0 {
		return 32000
	}
	return n
}
func normalizeJSONObject(raw string) []byte {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []byte("{}")
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return []byte("{}")
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return []byte("{}")
	}
	return out
}
func emptyAnthropicUserMessage() apicompat.AnthropicMessage {
	return apicompat.AnthropicMessage{
		Role:    "user",
		Content: mustMarshal([]apicompat.AnthropicContentBlock{{Type: "text", Text: ""}}),
	}
}
func buildRawImageBlock(raw string, fallbackMediaType string) *apicompat.AnthropicContentBlock {
	data, mediaType := normalizeBase64Image(raw, fallbackMediaType)
	if data == "" {
		return nil
	}
	if mediaType == "" {
		mediaType = defaultImageMediaType
	}
	return &apicompat.AnthropicContentBlock{Type: "image", Source: &apicompat.AnthropicImageSource{Type: "base64", MediaType: mediaType, Data: data}}
}
func normalizeBase64Image(raw, fallbackMediaType string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if data, mediaType, ok := splitDataURLBase64(raw); ok {
		if mediaType == "" {
			mediaType = fallbackMediaType
		}
		if mediaType == "" {
			mediaType = defaultImageMediaType
		}
		return data, mediaType
	}
	if fallbackMediaType == "" {
		fallbackMediaType = defaultImageMediaType
	}
	return raw, fallbackMediaType
}
func splitDataURLBase64(raw string) (string, string, bool) {
	if !strings.HasPrefix(strings.ToLower(raw), "data:") {
		return "", "", false
	}
	comma := strings.Index(raw, ",")
	if comma == -1 {
		return "", "", false
	}
	meta := raw[len("data:"):comma]
	if meta == "" || !strings.Contains(strings.ToLower(meta), "base64") {
		return "", "", false
	}
	segments := strings.Split(meta, ";")
	mediaType := ""
	if len(segments) > 0 {
		mediaType = strings.ToLower(strings.TrimSpace(segments[0]))
	}
	data := strings.TrimSpace(raw[comma+1:])
	if data == "" {
		return "", "", false
	}
	return data, mediaType, true
}
func coerceRulesText(rules interface{}) string {
	switch v := rules.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []string:
		return strings.TrimSpace(strings.Join(v, "\n"))
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
func personaTypeToLabel(v int) string {
	switch v {
	case 1:
		return "PROTOTYPER"
	case 2:
		return "BRAINSTORM"
	case 3:
		return "REVIEWER"
	default:
		return "DEFAULT"
	}
}
func decodeNodesFromInterface(raw interface{}) []Node {
	if raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var nodes []Node
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil
	}
	return nodes
}
func firstMapString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}
func emptyFallback(text string) string {
	if strings.TrimSpace(text) == "" {
		return "(unknown)"
	}
	return text
}
func isTextLikeMediaType(mediaType string) bool {
	mediaType = strings.TrimSpace(strings.ToLower(mediaType))
	return strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || mediaType == "application/xml" || mediaType == "application/yaml" || mediaType == "application/x-yaml" || mediaType == "application/markdown"
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func toolResultContentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return strings.TrimSpace(string(raw))
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, text)
			}
		case "image":
			parts = append(parts, "[image omitted]")
		}
	}
	return strings.Join(parts, "\n")
}
func repairAnthropicToolUsePairs(messages []apicompat.AnthropicMessage) []apicompat.AnthropicMessage {
	if len(messages) == 0 {
		return messages
	}

	type pendingToolUse struct {
		ID   string
		Name string
	}

	out := make([]apicompat.AnthropicMessage, 0, len(messages)+2)
	pending := make([]pendingToolUse, 0)

	injectMissing := func() {
		if len(pending) == 0 {
			return
		}
		blocks := make([]apicompat.AnthropicContentBlock, 0, len(pending))
		for _, item := range pending {
			blocks = append(blocks, apicompat.AnthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: item.ID,
				Content:   mustMarshal(fmt.Sprintf("[tool_result not available: tool_use_id=%s tool_name=%s]", item.ID, item.Name)),
				IsError:   true,
			})
		}
		out = append(out, apicompat.AnthropicMessage{Role: "user", Content: mustMarshal(blocks)})
		pending = nil
	}

	for _, msg := range messages {
		blocks, ok := parseAnthropicContentBlocks(msg.Content)

		if len(pending) > 0 && msg.Role == "user" {
			if !ok {
				injectMissing()
				out = append(out, msg)
				continue
			}

			pendingMap := make(map[string]string, len(pending))
			for _, item := range pending {
				pendingMap[item.ID] = item.Name
			}

			toolResults := make([]apicompat.AnthropicContentBlock, 0)
			otherBlocks := make([]apicompat.AnthropicContentBlock, 0, len(blocks))
			changed := false

			for _, block := range blocks {
				if block.Type != "tool_result" {
					otherBlocks = append(otherBlocks, block)
					continue
				}
				toolUseID := strings.TrimSpace(block.ToolUseID)
				if _, found := pendingMap[toolUseID]; found {
					delete(pendingMap, toolUseID)
					toolResults = append(toolResults, block)
					continue
				}
				otherBlocks = append(otherBlocks, apicompat.AnthropicContentBlock{
					Type: "text",
					Text: buildOrphanToolResultText(block),
				})
				changed = true
			}

			for _, item := range pending {
				if _, stillPending := pendingMap[item.ID]; stillPending {
					toolResults = append(toolResults, apicompat.AnthropicContentBlock{
						Type:      "tool_result",
						ToolUseID: item.ID,
						Content:   mustMarshal(fmt.Sprintf("[tool_result not available: tool_use_id=%s tool_name=%s]", item.ID, item.Name)),
						IsError:   true,
					})
					changed = true
				}
			}
			pending = nil

			if len(toolResults) > 0 || changed {
				msg.Content = mustMarshal(append(toolResults, otherBlocks...))
			}
			out = append(out, msg)
			continue
		}

		if len(pending) > 0 {
			injectMissing()
		}

		if msg.Role == "user" && ok {
			changed := false
			rewritten := make([]apicompat.AnthropicContentBlock, 0, len(blocks))
			for _, block := range blocks {
				if block.Type == "tool_result" {
					rewritten = append(rewritten, apicompat.AnthropicContentBlock{
						Type: "text",
						Text: buildOrphanToolResultText(block),
					})
					changed = true
					continue
				}
				rewritten = append(rewritten, block)
			}
			if changed {
				msg.Content = mustMarshal(rewritten)
			}
			out = append(out, msg)
			continue
		}

		out = append(out, msg)
		if msg.Role != "assistant" || !ok {
			continue
		}
		for _, block := range blocks {
			if block.Type != "tool_use" {
				continue
			}
			id := strings.TrimSpace(block.ID)
			name := strings.TrimSpace(block.Name)
			if id == "" || name == "" {
				continue
			}
			pending = append(pending, pendingToolUse{ID: id, Name: name})
		}
	}

	injectMissing()
	return out
}

func parseAnthropicContentBlocks(raw json.RawMessage) ([]apicompat.AnthropicContentBlock, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, false
	}
	return blocks, true
}

func buildOrphanToolResultText(block apicompat.AnthropicContentBlock) string {
	header := "[orphan_tool_result"
	if id := strings.TrimSpace(block.ToolUseID); id != "" {
		header += " tool_use_id=" + id
	}
	if block.IsError {
		header += " is_error=true"
	}
	header += "]"
	body := strings.TrimSpace(toolResultContentText(block.Content))
	if body == "" {
		return header
	}
	return header + "\n" + body
}
func mustMarshal(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage([]byte("null"))
	}
	return data
}
func stringValue(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return ""
	}
}
func intValue(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int8:
		return int(t)
	case int16:
		return int(t)
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float32:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		i, _ := t.Int64()
		return int(i)
	default:
		return 0
	}
}
