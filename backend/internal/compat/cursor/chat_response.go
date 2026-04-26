package cursor

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

func PatchChatResponseBody(raw []byte, clientModel string) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if clientModel != "" {
		payload["model"] = clientModel
	}
	if choices, ok := payload["choices"].([]any); ok {
		for _, choiceValue := range choices {
			choice, ok := choiceValue.(map[string]any)
			if !ok {
				continue
			}
			fixChatResponseChoice(choice)
		}
	}
	return json.Marshal(payload)
}

func fixChatResponseChoice(choice map[string]any) {
	message, ok := choice["message"].(map[string]any)
	if !ok {
		return
	}
	promoteChatReasoningField(message)
	extractThinkTagsFromChatMessage(message)
	convertLegacyChatFunctionCall(message, choice)
	fixChatToolCalls(message, choice)
	rewriteChatFinishReason(choice)
}

func extractThinkTagsFromChatMessage(message map[string]any) {
	content, ok := message["content"].(string)
	if !ok || content == "" {
		return
	}
	if _, exists := message["reasoning_content"]; exists {
		return
	}
	cleaned, reasoning := extractChatThinkTags(content)
	if reasoning == "" {
		return
	}
	message["reasoning_content"] = reasoning
	message["content"] = cleaned
}

func extractChatThinkTags(text string) (string, string) {
	cleaned := text
	reasoningParts := make([]string, 0)
	for {
		start := strings.Index(cleaned, "<think>")
		if start < 0 {
			break
		}
		endOffset := strings.Index(cleaned[start+len("<think>"):], "</think>")
		if endOffset < 0 {
			break
		}
		end := start + len("<think>") + endOffset
		reasoningParts = append(reasoningParts, cleaned[start+len("<think>"):end])
		cleaned = cleaned[:start] + cleaned[end+len("</think>"):]
	}
	return strings.TrimSpace(cleaned), strings.TrimSpace(strings.Join(reasoningParts, "\n"))
}

func convertLegacyChatFunctionCall(message map[string]any, choice map[string]any) {
	if _, ok := message["tool_calls"]; ok {
		return
	}
	functionCall, ok := message["function_call"].(map[string]any)
	if !ok {
		return
	}
	message["tool_calls"] = []any{
		map[string]any{
			"id":   "call_" + uuid.NewString(),
			"type": "function",
			"function": map[string]any{
				"name":      strings.TrimSpace(messagesStringValue(functionCall["name"])),
				"arguments": strings.TrimSpace(messagesStringValue(functionCall["arguments"])),
			},
		},
	}
	functionData := message["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
	normalizeCursorFunctionArguments(functionData)
	unwrapCursorApplyPatchFunctionArguments(functionData)
	delete(message, "function_call")
	rewriteChatFinishReason(choice)
}

func fixChatToolCalls(message map[string]any, choice map[string]any) {
	toolCalls, ok := message["tool_calls"].([]any)
	if !ok || len(toolCalls) == 0 {
		return
	}
	for index, toolCallValue := range toolCalls {
		toolCall, ok := toolCallValue.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(messagesStringValue(toolCall["id"])) == "" {
			toolCall["id"] = "call_" + uuid.NewString()
		}
		if _, ok := toolCall["index"]; !ok {
			toolCall["index"] = index
		}
		if strings.TrimSpace(messagesStringValue(toolCall["type"])) != "function" {
			toolCall["type"] = "function"
		}
		functionData, _ := toolCall["function"].(map[string]any)
		if functionData == nil {
			functionData = map[string]any{}
			toolCall["function"] = functionData
		}
		switch arguments := functionData["arguments"].(type) {
		case map[string]any, []any:
			encoded, _ := json.Marshal(arguments)
			functionData["arguments"] = string(encoded)
		case nil:
			functionData["arguments"] = "{}"
		}
		normalizeCursorFunctionArguments(functionData)
		unwrapCursorApplyPatchFunctionArguments(functionData)
	}
	if strings.TrimSpace(messagesStringValue(choice["finish_reason"])) != "tool_calls" {
		choice["finish_reason"] = "tool_calls"
	}
}
