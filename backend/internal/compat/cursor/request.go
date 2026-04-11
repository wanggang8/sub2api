package cursor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// Cursor compat stays stateless on purpose and does not persist hidden reasoning caches.
func NormalizeResponsesRequestBody(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}

	var probe struct {
		Input    json.RawMessage `json:"input"`
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, err
	}
	if len(probe.Input) > 0 || len(probe.Messages) == 0 {
		return raw, nil
	}

	var req apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("parse chat payload: %w", err)
	}
	converted, err := chatCompletionsRequestToResponsesRequest(&req)
	if err != nil {
		return nil, fmt.Errorf("convert chat payload to responses: %w", err)
	}
	return json.Marshal(converted)
}

func NormalizeChatCompletionsRequestBody(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if _, ok := payload["messages"]; ok {
		return raw, nil
	}
	inputRaw, ok := payload["input"]
	if !ok {
		return raw, nil
	}

	messages, err := responsesInputToChatMessages(inputRaw)
	if err != nil {
		return nil, err
	}
	if instructionsRaw, ok := payload["instructions"]; ok {
		if instructions := strings.TrimSpace(unmarshalStringOrEmpty(instructionsRaw)); instructions != "" {
			messages = append([]apicompat.ChatMessage{{Role: "system", Content: mustMarshal(instructions)}}, messages...)
		}
		delete(payload, "instructions")
	}
	payload["messages"] = mustMarshal(messages)
	delete(payload, "input")

	if maxOutputTokens, ok := payload["max_output_tokens"]; ok {
		if _, exists := payload["max_completion_tokens"]; !exists {
			payload["max_completion_tokens"] = maxOutputTokens
		}
		delete(payload, "max_output_tokens")
	}

	if reasoningRaw, ok := payload["reasoning"]; ok {
		reasoningEffort, hasEffort, err := normalizeResponsesReasoningEffort(reasoningRaw)
		if err != nil {
			return nil, err
		}
		if hasEffort {
			if _, exists := payload["reasoning_effort"]; !exists {
				payload["reasoning_effort"] = reasoningEffort
			}
		}
		delete(payload, "reasoning")
	}

	if toolsRaw, ok := payload["tools"]; ok {
		normalizedTools, err := normalizeResponsesToolsToChatTools(toolsRaw)
		if err != nil {
			return nil, err
		}
		payload["tools"] = normalizedTools
	}

	if toolChoiceRaw, ok := payload["tool_choice"]; ok {
		normalizedToolChoice, err := normalizeResponsesToolChoiceToChatToolChoice(toolChoiceRaw)
		if err != nil {
			return nil, err
		}
		payload["tool_choice"] = normalizedToolChoice
	}

	return json.Marshal(payload)
}

func normalizeResponsesReasoningEffort(raw json.RawMessage) (json.RawMessage, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var reasoning struct {
		Effort string `json:"effort"`
	}
	if err := json.Unmarshal(raw, &reasoning); err != nil {
		return nil, false, fmt.Errorf("parse responses reasoning: %w", err)
	}
	if reasoning.Effort == "" {
		return nil, false, nil
	}
	return mustMarshal(reasoning.Effort), true, nil
}

func normalizeResponsesToolsToChatTools(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return raw, nil
	}

	var probe []map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("parse responses tools: %w", err)
	}
	for _, tool := range probe {
		if _, ok := tool["function"]; ok {
			return raw, nil
		}
		if toolType, _ := tool["type"].(string); toolType != "function" {
			return raw, nil
		}
	}

	var responseTools []apicompat.ResponsesTool
	if err := json.Unmarshal(raw, &responseTools); err != nil {
		return nil, fmt.Errorf("parse responses tools: %w", err)
	}
	chatTools := make([]apicompat.ChatTool, 0, len(responseTools))
	for _, tool := range responseTools {
		chatTools = append(chatTools, apicompat.ChatTool{
			Type: "function",
			Function: &apicompat.ChatFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
				Strict:      tool.Strict,
			},
		})
	}
	return mustMarshal(chatTools), nil
}

func normalizeResponsesToolChoiceToChatToolChoice(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return raw, nil
	}

	var choiceText string
	if err := json.Unmarshal(raw, &choiceText); err == nil {
		return raw, nil
	}

	var choice map[string]any
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil, fmt.Errorf("parse responses tool_choice: %w", err)
	}
	if _, ok := choice["function"]; ok {
		return raw, nil
	}
	if choiceType, _ := choice["type"].(string); choiceType != "function" {
		return raw, nil
	}
	name, _ := choice["name"].(string)
	if name == "" {
		return raw, nil
	}
	return mustMarshal(map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": name,
		},
	}), nil
}

func NormalizeMessagesRequestBody(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	changed := normalizeClaudeMessageToolUseInputs(payload)
	changed = normalizeClaudeToolSchemas(payload) || changed
	if !changed {
		return raw, nil
	}
	return json.Marshal(payload)
}

func normalizeClaudeToolSchemas(payload map[string]any) bool {
	rawTools, ok := payload["tools"].([]any)
	if !ok || len(rawTools) == 0 {
		return false
	}

	changed := false
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		if schema, exists := tool["input_schema"]; exists && schema != nil {
			continue
		}
		tool["input_schema"] = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
		changed = true
	}
	return changed
}

func normalizeClaudeMessageToolUseInputs(payload map[string]any) bool {
	rawMessages, ok := payload["messages"].([]any)
	if !ok || len(rawMessages) == 0 {
		return false
	}

	changed := false
	for _, rawMessage := range rawMessages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		content, ok := message["content"].([]any)
		if !ok || len(content) == 0 {
			continue
		}
		for _, rawBlock := range content {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				continue
			}
			if blockType, _ := block["type"].(string); blockType != "tool_use" {
				continue
			}
			switch input := block["input"].(type) {
			case nil:
				block["input"] = map[string]any{}
				changed = true
			case string:
				if strings.TrimSpace(input) == "" {
					block["input"] = map[string]any{}
					changed = true
				}
			}
		}
	}
	return changed
}

func PatchMessagesResponseBody(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}

	reasoning, hadReasoning, err := extractMessagesReasoningField(payload)
	if err != nil {
		return nil, err
	}
	if !hadReasoning {
		return raw, nil
	}

	content, err := parseMessagesContentBlocks(payload["content"])
	if err != nil {
		return nil, err
	}
	if reasoning != "" && !messagesContentHasThinkingBlock(content) {
		content = append([]any{map[string]any{
			"type":     "thinking",
			"thinking": reasoning,
		}}, content...)
	}
	payload["content"] = mustMarshal(content)
	return json.Marshal(payload)
}

func extractMessagesReasoningField(payload map[string]json.RawMessage) (string, bool, error) {
	if len(payload) == 0 {
		return "", false, nil
	}

	hadReasoning := false
	reasoning := ""
	for _, key := range []string{"reasoning_content", "reasoningContent"} {
		raw, ok := payload[key]
		if !ok {
			continue
		}
		hadReasoning = true
		delete(payload, key)
		if len(raw) == 0 || string(raw) == "null" || reasoning != "" {
			continue
		}
		if err := json.Unmarshal(raw, &reasoning); err != nil {
			return "", false, fmt.Errorf("parse %s: %w", key, err)
		}
	}
	return reasoning, hadReasoning, nil
}

func parseMessagesContentBlocks(raw json.RawMessage) ([]any, error) {
	if len(raw) == 0 {
		return []any{}, nil
	}
	var content []any
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil, fmt.Errorf("parse messages content: %w", err)
	}
	return content, nil
}

func messagesContentHasThinkingBlock(content []any) bool {
	for _, item := range content {
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if blockType, _ := block["type"].(string); blockType == "thinking" {
			return true
		}
	}
	return false
}

func responsesInputToChatMessages(raw json.RawMessage) ([]apicompat.ChatMessage, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []apicompat.ChatMessage{{Role: "user", Content: mustMarshal(text)}}, nil
	}

	var items []cursorResponsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parse responses input: %w", err)
	}

	messages := make([]apicompat.ChatMessage, 0, len(items))
	pendingReasoning := ""
	for i := 0; i < len(items); {
		item := items[i]
		switch {
		case item.Type == "reasoning":
			pendingReasoning = responsesReasoningText(item)
			i++
		case item.Role != "" && item.Type == "":
			msg, err := responsesRoleItemToChatMessage(item)
			if err != nil {
				return nil, err
			}
			if msg.Role == "assistant" && pendingReasoning != "" {
				msg.ReasoningContent = pendingReasoning
				pendingReasoning = ""
			}
			messages = append(messages, msg)
			i++
		case item.Type == "message":
			msg, consumed, err := appendResponsesMessageItem(items, i)
			if err != nil {
				return nil, err
			}
			if msg.Role == "assistant" && pendingReasoning != "" {
				msg.ReasoningContent = pendingReasoning
				pendingReasoning = ""
			}
			messages = append(messages, msg)
			i += consumed
		case item.Type == "function_call":
			idx := appendResponsesFunctionCallItem(item, &messages)
			if pendingReasoning != "" && idx >= 0 {
				messages[idx].ReasoningContent = pendingReasoning
				pendingReasoning = ""
			}
			i++
		case item.Type == "function_call_output":
			messages = append(messages, apicompat.ChatMessage{
				Role:       "tool",
				ToolCallID: item.CallID,
				Content:    mustMarshal(item.Output),
			})
			i++
		default:
			if item.Role != "" {
				content, _, err := responsesContentToChatMessageParts(item.Content)
				if err != nil {
					return nil, err
				}
				messages = append(messages, apicompat.ChatMessage{Role: item.Role, Content: content})
			}
			i++
		}
	}
	return messages, nil
}

type cursorResponsesInputItem struct {
	Type      string                       `json:"type,omitempty"`
	Role      string                       `json:"role,omitempty"`
	Content   json.RawMessage              `json:"content,omitempty"`
	CallID    string                       `json:"call_id,omitempty"`
	Name      string                       `json:"name,omitempty"`
	Arguments string                       `json:"arguments,omitempty"`
	ID        string                       `json:"id,omitempty"`
	Output    string                       `json:"output,omitempty"`
	Summary   []apicompat.ResponsesSummary `json:"summary,omitempty"`
}

func findOrAppendAssistantMessage(messages *[]apicompat.ChatMessage) int {
	if messages == nil {
		return 0
	}
	for i := len(*messages) - 1; i >= 0; i-- {
		if (*messages)[i].Role == "assistant" {
			return i
		}
	}
	*messages = append(*messages, apicompat.ChatMessage{Role: "assistant"})
	return len(*messages) - 1
}

func responsesContentToChatMessageParts(raw json.RawMessage) (json.RawMessage, string, error) {
	if len(raw) == 0 {
		return nil, "", nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return mustMarshal(text), "", nil
	}

	var parts []apicompat.ResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, "", fmt.Errorf("parse responses content: %w", err)
	}

	chatParts := make([]apicompat.ChatContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_image":
			chatParts = append(chatParts, apicompat.ChatContentPart{
				Type: "image_url",
				ImageURL: &apicompat.ChatImageURL{
					URL: part.ImageURL,
				},
			})
		default:
			if part.Text == "" {
				continue
			}
			chatParts = append(chatParts, apicompat.ChatContentPart{Type: "text", Text: part.Text})
		}
	}
	if len(chatParts) == 0 {
		return mustMarshal(""), "", nil
	}
	if len(chatParts) == 1 && chatParts[0].Type == "text" {
		return mustMarshal(chatParts[0].Text), "", nil
	}
	return mustMarshal(chatParts), "", nil
}

func chatCompletionsRequestToResponsesRequest(req *apicompat.ChatCompletionsRequest) (*apicompat.ResponsesRequest, error) {
	if req == nil {
		return &apicompat.ResponsesRequest{}, nil
	}
	instructions := make([]string, 0, len(req.Messages))
	inputItems := make([]apicompat.ResponsesInputItem, 0, len(req.Messages))
	for _, message := range req.Messages {
		if strings.TrimSpace(message.Role) == "system" {
			text := strings.TrimSpace(chatMessageContentToText(message.Content))
			if text != "" {
				instructions = append(instructions, text)
			}
			continue
		}
		items, err := chatMessageToResponsesInputItems(message)
		if err != nil {
			return nil, err
		}
		inputItems = append(inputItems, items...)
	}
	inputJSON, err := json.Marshal(inputItems)
	if err != nil {
		return nil, fmt.Errorf("marshal responses input: %w", err)
	}
	converted := &apicompat.ResponsesRequest{
		Model:       req.Model,
		Input:       inputJSON,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		ToolChoice:  req.ToolChoice,
		ServiceTier: req.ServiceTier,
		Include:     []string{"reasoning.encrypted_content"},
	}
	if len(converted.ToolChoice) == 0 && len(req.FunctionCall) > 0 {
		converted.ToolChoice = normalizeLegacyFunctionCallToResponsesToolChoice(req.FunctionCall)
	}
	storeFalse := false
	converted.Store = &storeFalse
	if len(instructions) > 0 {
		converted.Instructions = strings.Join(instructions, "\n\n")
	}
	if req.MaxCompletionTokens != nil {
		v := *req.MaxCompletionTokens
		if v < 128 {
			v = 128
		}
		converted.MaxOutputTokens = &v
	} else if req.MaxTokens != nil {
		v := *req.MaxTokens
		if v < 128 {
			v = 128
		}
		converted.MaxOutputTokens = &v
	}
	if req.ReasoningEffort != "" {
		converted.Reasoning = &apicompat.ResponsesReasoning{Effort: req.ReasoningEffort, Summary: "auto"}
	}
	if len(req.Tools) > 0 || len(req.Functions) > 0 {
		converted.Tools = chatToolsToResponsesTools(req.Tools, req.Functions)
	}
	return converted, nil
}

func chatMessageToResponsesInputItems(message apicompat.ChatMessage) ([]apicompat.ResponsesInputItem, error) {
	switch strings.TrimSpace(message.Role) {
	case "tool":
		return []apicompat.ResponsesInputItem{{
			Type:   "function_call_output",
			CallID: message.ToolCallID,
			Output: chatMessageContentToText(message.Content),
		}}, nil
	case "assistant":
		items := make([]apicompat.ResponsesInputItem, 0, 3+len(message.ToolCalls))
		if reasoning := strings.TrimSpace(message.ReasoningContent); reasoning != "" {
			items = append(items, apicompat.ResponsesInputItem{
				Type:    "reasoning",
				Summary: []apicompat.ResponsesSummary{{Type: "summary_text", Text: reasoning}},
			})
		}
		if text := strings.TrimSpace(chatMessageContentToText(message.Content)); text != "" {
			items = append(items, apicompat.ResponsesInputItem{
				Type:    "message",
				Role:    "assistant",
				Content: mustMarshal([]apicompat.ResponsesContentPart{{Type: "output_text", Text: text}}),
			})
		}
		if message.FunctionCall != nil {
			items = append(items, apicompat.ResponsesInputItem{
				Type:      "function_call",
				CallID:    defaultString(message.Name, message.FunctionCall.Name),
				Name:      message.FunctionCall.Name,
				Arguments: defaultString(message.FunctionCall.Arguments, "{}"),
			})
		}
		for _, toolCall := range message.ToolCalls {
			items = append(items, apicompat.ResponsesInputItem{
				Type:      "function_call",
				CallID:    toolCall.ID,
				Name:      toolCall.Function.Name,
				Arguments: defaultString(toolCall.Function.Arguments, "{}"),
			})
		}
		return items, nil
	case "function":
		return []apicompat.ResponsesInputItem{{
			Type:   "function_call_output",
			CallID: message.Name,
			Output: chatMessageContentToText(message.Content),
		}}, nil
	default:
		content := chatMessageContentToText(message.Content)
		return []apicompat.ResponsesInputItem{{
			Role:    defaultString(message.Role, "user"),
			Content: mustMarshal(content),
		}}, nil
	}
}

func chatToolsToResponsesTools(tools []apicompat.ChatTool, functions []apicompat.ChatFunction) []apicompat.ResponsesTool {
	if len(tools) == 0 && len(functions) == 0 {
		return nil
	}
	converted := make([]apicompat.ResponsesTool, 0, len(tools)+len(functions))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Type) != "function" || tool.Function == nil {
			continue
		}
		converted = append(converted, apicompat.ResponsesTool{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
			Strict:      tool.Function.Strict,
		})
	}
	for _, function := range functions {
		converted = append(converted, apicompat.ResponsesTool{
			Type:        "function",
			Name:        function.Name,
			Description: function.Description,
			Parameters:  function.Parameters,
			Strict:      function.Strict,
		})
	}
	if len(converted) == 0 {
		return nil
	}
	return converted
}

func normalizeLegacyFunctionCallToResponsesToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return mustMarshal(text)
	}
	var payload struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || strings.TrimSpace(payload.Name) == "" {
		return nil
	}
	return mustMarshal(map[string]any{
		"type":     "function",
		"function": map[string]string{"name": payload.Name},
	})
}

func chatMessageContentToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []apicompat.ChatContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == "text" && part.Text != "" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func responsesRoleItemToChatMessage(item cursorResponsesInputItem) (apicompat.ChatMessage, error) {
	content, _, err := responsesContentToChatMessageParts(item.Content)
	if err != nil {
		return apicompat.ChatMessage{}, err
	}
	role := item.Role
	if role == "" {
		role = "user"
	}
	return apicompat.ChatMessage{Role: role, Content: content}, nil
}

func appendResponsesMessageItem(items []cursorResponsesInputItem, start int) (apicompat.ChatMessage, int, error) {
	item := items[start]
	content, _, err := responsesContentToChatMessageParts(item.Content)
	if err != nil {
		return apicompat.ChatMessage{}, 0, err
	}
	message := apicompat.ChatMessage{Role: defaultString(item.Role, "assistant"), Content: content}
	consumed := 1
	if message.Role == "assistant" {
		for start+consumed < len(items) && items[start+consumed].Type == "function_call" {
			next := items[start+consumed]
			message.ToolCalls = append(message.ToolCalls, apicompat.ChatToolCall{
				ID:   next.CallID,
				Type: "function",
				Function: apicompat.ChatFunctionCall{
					Name:      next.Name,
					Arguments: defaultString(next.Arguments, "{}"),
				},
			})
			consumed++
		}
		if len(message.ToolCalls) > 0 && len(message.Content) == 0 {
			message.Content = mustMarshal(nil)
		}
	}
	return message, consumed, nil
}

func appendResponsesFunctionCallItem(item cursorResponsesInputItem, messages *[]apicompat.ChatMessage) int {
	if messages == nil {
		return -1
	}
	toolCall := apicompat.ChatToolCall{
		ID:   item.CallID,
		Type: "function",
		Function: apicompat.ChatFunctionCall{
			Name:      item.Name,
			Arguments: defaultString(item.Arguments, "{}"),
		},
	}
	if len(*messages) > 0 && (*messages)[len(*messages)-1].Role == "assistant" {
		idx := len(*messages) - 1
		(*messages)[idx].ToolCalls = append((*messages)[idx].ToolCalls, toolCall)
		if len((*messages)[idx].Content) == 0 {
			(*messages)[idx].Content = mustMarshal(nil)
		}
		return idx
	}
	*messages = append(*messages, apicompat.ChatMessage{Role: "assistant", Content: mustMarshal(nil), ToolCalls: []apicompat.ChatToolCall{toolCall}})
	return len(*messages) - 1
}

func responsesReasoningText(item cursorResponsesInputItem) string {
	var builder strings.Builder
	for _, part := range item.Summary {
		if part.Type == "summary_text" && part.Text != "" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func unmarshalStringOrEmpty(raw json.RawMessage) string {
	var text string
	_ = json.Unmarshal(raw, &text)
	return text
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
