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

		var toolsChanged bool
		normalizedBody, toolsChanged, err = normalizeCursorResponsesEditingTools(normalizedBody)
		if err != nil {
			return nil, err
		}
		if changed || toolsChanged {
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

func normalizeCursorResponsesEditingTools(raw []byte) ([]byte, bool, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, false, err
	}
	if _, ok := payload["input"].([]any); !ok {
		return raw, false, nil
	}

	rawTools, _ := payload["tools"].([]any)
	normalizedTools := make([]any, 0, len(rawTools)+2)
	nestedFunctionStyle := false
	hasWrite := false
	hasStrReplace := false
	changed := false

	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			normalizedTools = append(normalizedTools, rawTool)
			continue
		}
		if _, ok := tool["function"].(map[string]any); ok {
			nestedFunctionStyle = true
		}
		name := cursorToolName(tool)
		if isCursorApplyPatchToolName(name) {
			changed = true
			continue
		}
		switch name {
		case "Write":
			hasWrite = true
		case "StrReplace":
			hasStrReplace = true
		}
		normalizedTools = append(normalizedTools, rawTool)
	}

	if !hasWrite {
		normalizedTools = append(normalizedTools, cursorEditingToolDefinition("Write", nestedFunctionStyle))
		changed = true
	}
	if !hasStrReplace {
		normalizedTools = append(normalizedTools, cursorEditingToolDefinition("StrReplace", nestedFunctionStyle))
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	payload["tools"] = normalizedTools
	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	return normalized, true, nil
}

func cursorToolName(tool map[string]any) string {
	if fn, ok := tool["function"].(map[string]any); ok {
		if name, _ := fn["name"].(string); strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	if name, _ := tool["name"].(string); strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return ""
}

func isCursorApplyPatchToolName(name string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(name)))
	return normalized == "applypatch"
}

func cursorEditingToolDefinition(name string, nestedFunctionStyle bool) map[string]any {
	description, parameters := cursorEditingToolSchema(name)
	if nestedFunctionStyle {
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        name,
				"description": description,
				"parameters":  parameters,
			},
		}
	}
	return map[string]any{
		"type":        "function",
		"name":        name,
		"description": description,
		"parameters":  parameters,
	}
}

func cursorEditingToolSchema(name string) (string, map[string]any) {
	switch name {
	case "Write":
		return "Writes a file to the local filesystem.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The absolute path to the file to modify.",
				},
				"contents": map[string]any{
					"type":        "string",
					"description": "The contents to write to the file.",
				},
			},
			"required": []string{"path", "contents"},
		}
	case "StrReplace":
		return "Performs exact string replacements in files.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The absolute path to the file to modify.",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "The text to replace.",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "The text to replace it with.",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "Replace all occurrences of old_string.",
				},
			},
			"required": []string{"path", "old_string", "new_string"},
		}
	default:
		return "", map[string]any{"type": "object", "properties": map[string]any{}}
	}
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
	normalizedTools := make([]any, 0, len(probe))
	changed := false
	for _, rawTool := range probe {
		toolType, _ := rawTool["type"].(string)
		if toolType != "" && toolType != "function" {
			normalizedTools = append(normalizedTools, rawTool)
			continue
		}
		if rawFn, ok := rawTool["function"]; ok {
			fnJSON, err := json.Marshal(rawFn)
			if err != nil {
				return nil, fmt.Errorf("marshal chat tool function: %w", err)
			}
			var fn apicompat.ChatFunction
			if err := json.Unmarshal(fnJSON, &fn); err != nil {
				return nil, fmt.Errorf("parse chat tool function: %w", err)
			}
			normalizedTools = append(normalizedTools, apicompat.ChatTool{
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
		name := strings.TrimSpace(unmarshalStringOrEmpty(tool["name"]))
		description := unmarshalStringOrEmpty(tool["description"])
		parameters := tool["parameters"]
		if len(parameters) == 0 {
			parameters = tool["input_schema"]
		}
		if len(parameters) == 0 {
			parameters = mustMarshal(map[string]any{"type": "object", "properties": map[string]any{}})
		}
		var strict *bool
		if rawStrict, ok := tool["strict"]; ok && len(rawStrict) > 0 {
			var strictValue bool
			if err := json.Unmarshal(rawStrict, &strictValue); err == nil {
				strict = &strictValue
			}
		}
		normalizedTools = append(normalizedTools, apicompat.ChatTool{
			Type: "function",
			Function: &apicompat.ChatFunction{
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
	return mustMarshal(normalizedTools), nil
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
	if choiceType, _ := choice["type"].(string); choiceType == "any" {
		return mustMarshal("required"), nil
	} else if choiceType == "auto" || choiceType == "none" || choiceType == "required" {
		return mustMarshal(choiceType), nil
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
				Content:    mustMarshal(responsesFunctionOutputText(item.Output)),
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
	Type      string                   `json:"type,omitempty"`
	Role      string                   `json:"role,omitempty"`
	Content   json.RawMessage          `json:"content,omitempty"`
	CallID    string                   `json:"call_id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
	ID        string                   `json:"id,omitempty"`
	Output    json.RawMessage          `json:"output,omitempty"`
	Summary   []cursorResponsesSummary `json:"summary,omitempty"`
}

type cursorResponsesSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
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
				Type:     "image_url",
				ImageURL: &apicompat.ChatImageURL{URL: part.ImageURL},
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
				ID:       next.CallID,
				Type:     "function",
				Function: apicompat.ChatFunctionCall{Name: next.Name, Arguments: defaultString(next.Arguments, "{}")},
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
		ID:       item.CallID,
		Type:     "function",
		Function: apicompat.ChatFunctionCall{Name: item.Name, Arguments: defaultString(item.Arguments, "{}")},
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
