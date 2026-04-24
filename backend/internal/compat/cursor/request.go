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
	if len(probe.Input) > 0 {
		normalizedBody := raw
		changed := false

		normalizedInput, inputChanged, err := normalizeResponsesInputFunctionOutputs(probe.Input)
		if err != nil {
			return nil, err
		}
		if inputChanged {
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(raw, &payload); err != nil {
				return nil, err
			}
			payload["input"] = normalizedInput
			normalizedBody, err = json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			changed = true
		}

		if changed {
			return normalizedBody, nil
		}
		return raw, nil
	}
	if len(probe.Messages) == 0 {
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

func normalizeResponsesInputFunctionOutputs(raw json.RawMessage) (json.RawMessage, bool, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return raw, false, nil
	}

	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return raw, false, nil
	}

	changed := false
	for _, item := range items {
		if itemType, _ := item["type"].(string); itemType != "function_call_output" {
			continue
		}
		output, exists := item["output"]
		if !exists {
			continue
		}
		if _, ok := output.(string); ok {
			continue
		}
		outputJSON, err := json.Marshal(output)
		if err != nil {
			return nil, false, fmt.Errorf("marshal function_call_output output: %w", err)
		}
		item["output"] = responsesFunctionOutputText(outputJSON)
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	normalized, err := json.Marshal(items)
	if err != nil {
		return nil, false, fmt.Errorf("marshal normalized responses input: %w", err)
	}
	return normalized, true, nil
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
		normalized, changed, err := normalizeChatCompletionsMessagesPayload(payload)
		if err != nil {
			return nil, err
		}
		if changed {
			return normalized, nil
		}
		return raw, nil
	}
	if _, ok := payload["input"]; !ok {
		return raw, nil
	}
	return apicompat.NormalizeResponsesShapeChatCompletionsBody(raw)
}

func normalizeChatCompletionsMessagesPayload(payload map[string]json.RawMessage) ([]byte, bool, error) {
	if len(payload) == 0 || chatToolsContainName(payload["tools"], "multi_tool_use.parallel") {
		return nil, false, nil
	}
	messagesRaw, ok := payload["messages"]
	if !ok || len(messagesRaw) == 0 {
		return nil, false, nil
	}
	var messages []map[string]any
	if err := json.Unmarshal(messagesRaw, &messages); err != nil {
		return nil, false, fmt.Errorf("parse chat messages: %w", err)
	}
	changed := false
	for _, message := range messages {
		role, _ := message["role"].(string)
		if role != "system" && role != "developer" {
			continue
		}
		if normalizeParallelToolInstructionInMessage(message) {
			changed = true
		}
	}
	if !changed {
		return nil, false, nil
	}
	payload["messages"] = mustMarshal(messages)
	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil, false, fmt.Errorf("marshal normalized chat messages: %w", err)
	}
	return normalized, true, nil
}

func chatToolsContainName(raw json.RawMessage, name string) bool {
	if len(raw) == 0 || strings.TrimSpace(name) == "" {
		return false
	}
	var tools []map[string]any
	if err := json.Unmarshal(raw, &tools); err != nil {
		return false
	}
	for _, tool := range tools {
		if strings.TrimSpace(fmt.Sprint(tool["name"])) == name {
			return true
		}
		if fn, ok := tool["function"].(map[string]any); ok && strings.TrimSpace(fmt.Sprint(fn["name"])) == name {
			return true
		}
	}
	return false
}

func normalizeParallelToolInstructionInMessage(message map[string]any) bool {
	content, ok := message["content"]
	if !ok {
		return false
	}
	switch value := content.(type) {
	case string:
		normalized, changed := normalizeMissingParallelToolInstruction(value)
		if changed {
			message["content"] = normalized
		}
		return changed
	case []any:
		changed := false
		for _, rawBlock := range value {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				continue
			}
			text, ok := block["text"].(string)
			if !ok {
				continue
			}
			normalized, blockChanged := normalizeMissingParallelToolInstruction(text)
			if blockChanged {
				block["text"] = normalized
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func normalizeMissingParallelToolInstruction(text string) (string, bool) {
	if !strings.Contains(text, "multi_tool_use.parallel") {
		return text, false
	}
	normalized := strings.ReplaceAll(
		text,
		"Use `multi_tool_use.parallel` to parallelize tool calls and only this.",
		"Parallelize independent tool work by emitting multiple tool_calls in the same assistant response.",
	)
	normalized = strings.ReplaceAll(normalized, "`multi_tool_use.parallel`", "multiple tool_calls in the same assistant response")
	normalized = strings.ReplaceAll(normalized, "multi_tool_use.parallel", "multiple tool_calls in the same assistant response")
	return normalized, normalized != text
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

type cursorResponsesSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
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
				Type: "reasoning",
				Content: mustMarshal([]cursorResponsesSummary{{
					Type: "summary_text",
					Text: reasoning,
				}}),
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
		if strings.TrimSpace(tool.Type) != "function" {
			continue
		}
		fn := tool.Function
		if fn == nil {
			fn = &apicompat.ChatFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
				Strict:      tool.Strict,
			}
		}
		if strings.TrimSpace(fn.Name) == "" {
			continue
		}
		converted = append(converted, apicompat.ResponsesTool{
			Type:        "function",
			Name:        fn.Name,
			Description: fn.Description,
			Parameters:  fn.Parameters,
			Strict:      fn.Strict,
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

func responsesFunctionOutputText(raw json.RawMessage) string {
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
			if b, err := json.Marshal(part); err == nil {
				out = append(out, string(b))
			}
		}
		return strings.Join(out, "\n")
	}

	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		if b, err := json.Marshal(value); err == nil {
			return string(b)
		}
		return fmt.Sprint(value)
	}
	return string(raw)
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
