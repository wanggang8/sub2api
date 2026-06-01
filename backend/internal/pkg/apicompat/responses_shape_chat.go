package apicompat

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizeResponsesShapeChatCompletionsBody converts a Responses-shaped request
// body sent to a Chat Completions endpoint into a Chat Completions body.
func NormalizeResponsesShapeChatCompletionsBody(raw []byte) ([]byte, error) {
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

	messages, err := responsesShapeInputToChatMessages(inputRaw)
	if err != nil {
		return nil, err
	}
	if instructionsRaw, ok := payload["instructions"]; ok {
		if instructions := strings.TrimSpace(unmarshalResponsesShapeString(instructionsRaw)); instructions != "" {
			messages = append([]ChatMessage{{Role: "system", Content: marshalResponsesShapeJSON(instructions)}}, messages...)
		}
		delete(payload, "instructions")
	}
	payload["messages"] = marshalResponsesShapeJSON(messages)
	delete(payload, "input")

	if maxOutputTokens, ok := payload["max_output_tokens"]; ok {
		if _, exists := payload["max_completion_tokens"]; !exists {
			payload["max_completion_tokens"] = maxOutputTokens
		}
		delete(payload, "max_output_tokens")
	}

	if reasoningRaw, ok := payload["reasoning"]; ok {
		reasoningEffort, hasEffort, err := normalizeResponsesShapeReasoningEffort(reasoningRaw)
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
		normalizedTools, err := normalizeResponsesShapeToolsToChatTools(toolsRaw)
		if err != nil {
			return nil, err
		}
		payload["tools"] = normalizedTools
	}

	if toolChoiceRaw, ok := payload["tool_choice"]; ok {
		normalizedToolChoice, err := normalizeResponsesShapeToolChoiceToChatToolChoice(toolChoiceRaw)
		if err != nil {
			return nil, err
		}
		payload["tool_choice"] = normalizedToolChoice
	}

	return json.Marshal(payload)
}

func normalizeResponsesShapeReasoningEffort(raw json.RawMessage) (json.RawMessage, bool, error) {
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
	return marshalResponsesShapeJSON(reasoning.Effort), true, nil
}

func normalizeResponsesShapeToolsToChatTools(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return raw, nil
	}

	var probe []map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("parse responses tools: %w", err)
	}
	normalizedTools := make([]any, 0, len(probe))
	changed := false
	for _, rawTool := range probe {
		if rawFn, ok := rawTool["function"]; ok {
			fnJSON, err := json.Marshal(rawFn)
			if err != nil {
				return nil, fmt.Errorf("marshal chat tool function: %w", err)
			}
			var fn ChatFunction
			if err := json.Unmarshal(fnJSON, &fn); err != nil {
				return nil, fmt.Errorf("parse chat tool function: %w", err)
			}
			normalizedTools = append(normalizedTools, ChatTool{
				Type:     "function",
				Function: &fn,
			})
			continue
		}
		if name, _ := rawTool["name"].(string); strings.TrimSpace(name) == "" {
			normalizedTools = append(normalizedTools, rawTool)
			continue
		}

		toolJSON, err := json.Marshal(rawTool)
		if err != nil {
			return nil, fmt.Errorf("marshal responses tool: %w", err)
		}
		var tool map[string]json.RawMessage
		if err := json.Unmarshal(toolJSON, &tool); err != nil {
			return nil, fmt.Errorf("parse responses tool: %w", err)
		}
		name := strings.TrimSpace(unmarshalResponsesShapeString(tool["name"]))
		description := unmarshalResponsesShapeString(tool["description"])
		parameters := tool["parameters"]
		if len(parameters) == 0 {
			parameters = tool["input_schema"]
		}
		if len(parameters) == 0 {
			parameters = marshalResponsesShapeJSON(map[string]any{"type": "object", "properties": map[string]any{}})
		}
		var strict *bool
		if rawStrict, ok := tool["strict"]; ok && len(rawStrict) > 0 {
			var strictValue bool
			if err := json.Unmarshal(rawStrict, &strictValue); err == nil {
				strict = &strictValue
			}
		}
		normalizedTools = append(normalizedTools, ChatTool{
			Type: "function",
			Function: &ChatFunction{
				Name:        name,
				Description: description,
				Parameters:  parameters,
				Strict:      strict,
			},
		})
		changed = true
	}
	if !changed {
		return raw, nil
	}
	return marshalResponsesShapeJSON(normalizedTools), nil
}

func normalizeResponsesShapeToolChoiceToChatToolChoice(raw json.RawMessage) (json.RawMessage, error) {
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
	if choiceType, _ := choice["type"].(string); choiceType == "any" {
		return marshalResponsesShapeJSON("required"), nil
	} else if choiceType == "auto" || choiceType == "none" || choiceType == "required" {
		return marshalResponsesShapeJSON(choiceType), nil
	} else if choiceType != "function" && choiceType != "tool" {
		return raw, nil
	}
	name := ""
	if fn, ok := choice["function"].(map[string]any); ok {
		name, _ = fn["name"].(string)
	}
	if name == "" {
		name, _ = choice["name"].(string)
	}
	if name == "" {
		return raw, nil
	}
	return marshalResponsesShapeJSON(map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": name,
		},
	}), nil
}

func responsesShapeInputToChatMessages(raw json.RawMessage) ([]ChatMessage, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []ChatMessage{{Role: "user", Content: marshalResponsesShapeJSON(text)}}, nil
	}

	var items []responsesShapeInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("parse responses input: %w", err)
	}

	messages := make([]ChatMessage, 0, len(items))
	pendingReasoning := ""
	for i := 0; i < len(items); {
		item := items[i]
		switch {
		case item.Type == "reasoning":
			pendingReasoning = responsesShapeReasoningText(item)
			i++
		case item.Role != "" && item.Type == "":
			msg, err := responsesShapeRoleItemToChatMessage(item)
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
			msg, consumed, err := appendResponsesShapeMessageItem(items, i)
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
			idx := appendResponsesShapeFunctionCallItem(item, &messages)
			if pendingReasoning != "" && idx >= 0 {
				messages[idx].ReasoningContent = pendingReasoning
				pendingReasoning = ""
			}
			i++
		case item.Type == "function_call_output":
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: item.CallID,
				Content:    marshalResponsesShapeJSON(responsesShapeFunctionOutputText(item.Output)),
			})
			i++
		default:
			if item.Role != "" {
				content, _, err := responsesShapeContentToChatMessageParts(item.Content)
				if err != nil {
					return nil, err
				}
				messages = append(messages, ChatMessage{Role: item.Role, Content: content})
			}
			i++
		}
	}
	return messages, nil
}

type responsesShapeInputItem struct {
	Type      string                  `json:"type,omitempty"`
	Role      string                  `json:"role,omitempty"`
	Content   json.RawMessage         `json:"content,omitempty"`
	CallID    string                  `json:"call_id,omitempty"`
	Name      string                  `json:"name,omitempty"`
	Arguments string                  `json:"arguments,omitempty"`
	ID        string                  `json:"id,omitempty"`
	Output    json.RawMessage         `json:"output,omitempty"`
	Summary   []responsesShapeSummary `json:"summary,omitempty"`
}

type responsesShapeSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func responsesShapeContentToChatMessageParts(raw json.RawMessage) (json.RawMessage, string, error) {
	if len(raw) == 0 {
		return nil, "", nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return marshalResponsesShapeJSON(text), "", nil
	}

	var parts []ResponsesContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, "", fmt.Errorf("parse responses content: %w", err)
	}

	chatParts := make([]ChatContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "input_image":
			chatParts = append(chatParts, ChatContentPart{
				Type:     "image_url",
				ImageURL: &ChatImageURL{URL: part.ImageURL},
			})
		default:
			if part.Text == "" {
				continue
			}
			chatParts = append(chatParts, ChatContentPart{Type: "text", Text: part.Text})
		}
	}
	if len(chatParts) == 0 {
		return marshalResponsesShapeJSON(""), "", nil
	}
	if len(chatParts) == 1 && chatParts[0].Type == "text" {
		return marshalResponsesShapeJSON(chatParts[0].Text), "", nil
	}
	return marshalResponsesShapeJSON(chatParts), "", nil
}

func responsesShapeRoleItemToChatMessage(item responsesShapeInputItem) (ChatMessage, error) {
	content, _, err := responsesShapeContentToChatMessageParts(item.Content)
	if err != nil {
		return ChatMessage{}, err
	}
	role := item.Role
	if role == "" {
		role = "user"
	}
	return ChatMessage{Role: role, Content: content}, nil
}

func appendResponsesShapeMessageItem(items []responsesShapeInputItem, start int) (ChatMessage, int, error) {
	item := items[start]
	content, _, err := responsesShapeContentToChatMessageParts(item.Content)
	if err != nil {
		return ChatMessage{}, 0, err
	}
	message := ChatMessage{Role: defaultResponsesShapeString(item.Role, "assistant"), Content: content}
	consumed := 1
	if message.Role == "assistant" {
		for start+consumed < len(items) && items[start+consumed].Type == "function_call" {
			next := items[start+consumed]
			message.ToolCalls = append(message.ToolCalls, ChatToolCall{
				ID:       next.CallID,
				Type:     "function",
				Function: ChatFunctionCall{Name: next.Name, Arguments: defaultResponsesShapeString(next.Arguments, "{}")},
			})
			consumed++
		}
		if len(message.ToolCalls) > 0 && len(message.Content) == 0 {
			message.Content = marshalResponsesShapeJSON(nil)
		}
	}
	return message, consumed, nil
}

func appendResponsesShapeFunctionCallItem(item responsesShapeInputItem, messages *[]ChatMessage) int {
	if messages == nil {
		return -1
	}
	toolCall := ChatToolCall{
		ID:       item.CallID,
		Type:     "function",
		Function: ChatFunctionCall{Name: item.Name, Arguments: defaultResponsesShapeString(item.Arguments, "{}")},
	}
	if len(*messages) > 0 && (*messages)[len(*messages)-1].Role == "assistant" {
		idx := len(*messages) - 1
		(*messages)[idx].ToolCalls = append((*messages)[idx].ToolCalls, toolCall)
		if len((*messages)[idx].Content) == 0 {
			(*messages)[idx].Content = marshalResponsesShapeJSON(nil)
		}
		return idx
	}
	*messages = append(*messages, ChatMessage{Role: "assistant", Content: marshalResponsesShapeJSON(nil), ToolCalls: []ChatToolCall{toolCall}})
	return len(*messages) - 1
}

func responsesShapeReasoningText(item responsesShapeInputItem) string {
	parts := item.Summary
	if len(parts) == 0 && len(item.Content) > 0 {
		_ = json.Unmarshal(item.Content, &parts)
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == "summary_text" && part.Text != "" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

func responsesShapeFunctionOutputText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err == nil {
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if t, _ := part["text"].(string); strings.TrimSpace(t) != "" {
				out = append(out, t)
				continue
			}
			if t, _ := part["content"].(string); strings.TrimSpace(t) != "" {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			return strings.Join(out, "\n")
		}
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func unmarshalResponsesShapeString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return ""
}

func defaultResponsesShapeString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func marshalResponsesShapeJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}
