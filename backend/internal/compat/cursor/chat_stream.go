package cursor

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type ChatStreamState struct {
	InThinkingTag             bool
	ChatToolCallsSeen         bool
	ToolCallNames             map[int]string
	ApplyPatchArgumentBuffers map[int]*strings.Builder
}

func NewChatStreamState() *ChatStreamState {
	return &ChatStreamState{}
}

func PatchChatStreamChunk(chunk []byte, clientModel string, state *ChatStreamState) ([]byte, error) {
	if len(chunk) == 0 {
		return nil, nil
	}
	if state == nil {
		state = NewChatStreamState()
	}

	var output bytes.Buffer
	eventName, data, ok := parseMessagesSSEChunk(chunk)
	if !ok {
		output.Write(chunk)
		return output.Bytes(), nil
	}
	if data == "[DONE]" {
		if closeChunk := finalizeChatThinkingChunk(state, clientModel); len(closeChunk) > 0 {
			output.Write(closeChunk)
		}
		writeMessagesSSEChunk(&output, "", "[DONE]")
		return output.Bytes(), nil
	}

	payload, ok := decodeMessagesJSONObject([]byte(data))
	if !ok {
		output.Write(chunk)
		return output.Bytes(), nil
	}

	for _, item := range formatChatStreamEvent(eventName, payload, clientModel, state) {
		writeMessagesSSEChunk(&output, item.eventName, item.payload)
	}
	return output.Bytes(), nil
}

func FinalizeChatStream(clientModel string, state *ChatStreamState) []byte {
	return finalizeChatThinkingChunk(state, clientModel)
}

func finalizeChatThinkingChunk(state *ChatStreamState, model string) []byte {
	if state == nil || !state.InThinkingTag {
		return nil
	}
	state.InThinkingTag = false
	payload := map[string]any{
		"id":     "",
		"object": "chat.completion.chunk",
		"model":  model,
		"choices": []any{
			map[string]any{
				"index": 0,
				"delta": map[string]any{
					"content": "\n</think>\n\n",
				},
				"finish_reason": nil,
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return []byte("data: " + string(encoded) + "\n\n")
}

func formatChatStreamEvent(eventName string, payload map[string]any, clientModel string, state *ChatStreamState) []responsesSSEItem {
	payload = fixChatChunkPayload(payload, clientModel)
	choices, ok := payload["choices"].([]any)
	if !ok || len(choices) == 0 {
		return []responsesSSEItem{{eventName: eventName, payload: payload}}
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return []responsesSSEItem{{eventName: eventName, payload: payload}}
	}
	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		return []responsesSSEItem{{eventName: eventName, payload: payload}}
	}

	results := make([]responsesSSEItem, 0)
	finishReason := choice["finish_reason"]
	toolCalls, hasToolCalls := delta["tool_calls"].([]any)
	content, hasContent := delta["content"].(string)
	reasoning, hasReasoning := delta["reasoning_content"].(string)

	if hasContent && content != "" {
		results = append(results, splitChatContentIntoItems(payload, eventName, content, finishReason, hasToolCalls, state)...)
		delete(delta, "content")
	}
	if hasReasoning && reasoning != "" {
		results = append(results, responsesSSEItem{
			eventName: eventName,
			payload: cloneChatChunk(payload, map[string]any{
				"reasoning_content": reasoning,
			}, nil),
		})
		delete(delta, "reasoning_content")
	}
	if hasToolCalls && len(toolCalls) > 0 {
		toolCalls = bufferApplyPatchToolCallArguments(toolCalls, state)
		hasToolCalls = len(toolCalls) > 0
		if !hasToolCalls {
			delete(delta, "tool_calls")
		}
	}
	if hasToolCalls && len(toolCalls) > 0 {
		if !state.ChatToolCallsSeen {
			state.ChatToolCallsSeen = true
			if state.InThinkingTag {
				results = append(results, responsesSSEItem{
					eventName: eventName,
					payload: cloneChatChunk(payload, map[string]any{
						"content": "\n</think>\n\n",
					}, nil),
				})
				state.InThinkingTag = false
			} else if hasContent {
				results = append(results, responsesSSEItem{
					eventName: eventName,
					payload: cloneChatChunk(payload, map[string]any{
						"content": "\n",
					}, nil),
				})
			}
		} else if state.InThinkingTag {
			results = append(results, responsesSSEItem{
				eventName: eventName,
				payload: cloneChatChunk(payload, map[string]any{
					"content": "\n</think>\n\n",
				}, nil),
			})
			state.InThinkingTag = false
		}
		toolFinishReason := finishReason
		if finishReason != nil && hasBufferedApplyPatchToolCallArguments(state) {
			toolFinishReason = nil
		}
		toolPayload := cloneChatChunk(payload, map[string]any{
			"tool_calls": toolCalls,
		}, toolFinishReason)
		results = append(results, responsesSSEItem{eventName: eventName, payload: toolPayload})
		delete(delta, "tool_calls")
	}

	if finishReason != nil {
		flushed := flushApplyPatchToolCallArguments(payload, eventName, state)
		results = append(results, flushed...)
		if len(flushed) > 0 && len(delta) == 0 {
			results = append(results, responsesSSEItem{
				eventName: eventName,
				payload:   cloneChatChunk(payload, map[string]any{}, finishReason),
			})
		}
	}
	if len(delta) > 0 || finishReason != nil && len(results) == 0 {
		results = append(results, responsesSSEItem{eventName: eventName, payload: payload})
	}
	return results
}

func bufferApplyPatchToolCallArguments(toolCalls []any, state *ChatStreamState) []any {
	if state == nil {
		return toolCalls
	}
	kept := make([]any, 0, len(toolCalls))
	for _, rawToolCall := range toolCalls {
		toolCall, ok := rawToolCall.(map[string]any)
		if !ok {
			kept = append(kept, rawToolCall)
			continue
		}
		index := chatToolCallIndex(toolCall["index"])
		functionData, _ := toolCall["function"].(map[string]any)
		if functionData == nil {
			kept = append(kept, rawToolCall)
			continue
		}
		if name := strings.TrimSpace(messagesStringValue(functionData["name"])); name != "" {
			if state.ToolCallNames == nil {
				state.ToolCallNames = map[int]string{}
			}
			state.ToolCallNames[index] = name
		}
		if state.ToolCallNames[index] != "ApplyPatch" {
			kept = append(kept, rawToolCall)
			continue
		}
		arguments, hasArguments := functionData["arguments"].(string)
		if !hasArguments || arguments == "" {
			kept = append(kept, rawToolCall)
			continue
		}
		if state.ApplyPatchArgumentBuffers == nil {
			state.ApplyPatchArgumentBuffers = map[int]*strings.Builder{}
		}
		buffer := state.ApplyPatchArgumentBuffers[index]
		if buffer == nil {
			buffer = &strings.Builder{}
			state.ApplyPatchArgumentBuffers[index] = buffer
		}
		buffer.WriteString(arguments)

		cloned := cloneMessagesJSONObject(toolCall)
		clonedFunction, _ := cloned["function"].(map[string]any)
		if clonedFunction != nil {
			delete(clonedFunction, "arguments")
			if len(clonedFunction) == 0 {
				delete(cloned, "function")
			}
		}
		if chatToolCallDeltaHasPayload(cloned) {
			kept = append(kept, cloned)
		}
	}
	return kept
}

func flushApplyPatchToolCallArguments(template map[string]any, eventName string, state *ChatStreamState) []responsesSSEItem {
	if state == nil || len(state.ApplyPatchArgumentBuffers) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(state.ApplyPatchArgumentBuffers))
	for index, buffer := range state.ApplyPatchArgumentBuffers {
		if buffer != nil && buffer.Len() > 0 {
			indexes = append(indexes, index)
		}
	}
	sort.Ints(indexes)

	items := make([]responsesSSEItem, 0, len(indexes))
	for _, index := range indexes {
		buffer := state.ApplyPatchArgumentBuffers[index]
		if buffer == nil || buffer.Len() == 0 {
			continue
		}
		arguments := unwrapCursorApplyPatchArgumentsString(buffer.String())
		items = append(items, responsesSSEItem{
			eventName: eventName,
			payload: cloneChatChunk(template, map[string]any{
				"tool_calls": []any{
					map[string]any{
						"index": index,
						"function": map[string]any{
							"arguments": arguments,
						},
					},
				},
			}, nil),
		})
		delete(state.ApplyPatchArgumentBuffers, index)
		delete(state.ToolCallNames, index)
	}
	return items
}

func hasBufferedApplyPatchToolCallArguments(state *ChatStreamState) bool {
	if state == nil {
		return false
	}
	for _, buffer := range state.ApplyPatchArgumentBuffers {
		if buffer != nil && buffer.Len() > 0 {
			return true
		}
	}
	return false
}

func chatToolCallIndex(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		index, err := typed.Int64()
		if err == nil {
			return int(index)
		}
	}
	return 0
}

func chatToolCallDeltaHasPayload(toolCall map[string]any) bool {
	if toolCall == nil {
		return false
	}
	if strings.TrimSpace(messagesStringValue(toolCall["id"])) != "" || strings.TrimSpace(messagesStringValue(toolCall["type"])) != "" {
		return true
	}
	functionData, _ := toolCall["function"].(map[string]any)
	return functionData != nil && len(functionData) > 0
}

func splitChatContentIntoItems(template map[string]any, eventName, content string, finishReason any, hasToolCalls bool, state *ChatStreamState) []responsesSSEItem {
	items := make([]responsesSSEItem, 0)
	appendContent := func(text string) {
		if text == "" {
			return
		}
		items = append(items, responsesSSEItem{
			eventName: eventName,
			payload:   cloneChatChunk(template, map[string]any{"content": text}, nil),
		})
	}
	appendReasoning := func(text string) {
		if text == "" {
			return
		}
		items = append(items, responsesSSEItem{
			eventName: eventName,
			payload:   cloneChatChunk(template, map[string]any{"reasoning_content": text}, nil),
		})
	}

	remaining := content
	for len(remaining) > 0 {
		if state.InThinkingTag {
			closeIdx := strings.Index(remaining, "</think>")
			if closeIdx == -1 {
				appendReasoning(remaining)
				remaining = ""
				break
			}
			appendReasoning(remaining[:closeIdx])
			remaining = strings.TrimLeft(remaining[closeIdx+len("</think>"):], "\n")
			state.InThinkingTag = false
			continue
		}

		openIdx := strings.Index(remaining, "<think>")
		if openIdx == -1 {
			appendContent(remaining)
			remaining = ""
			break
		}
		if openIdx > 0 {
			appendContent(remaining[:openIdx])
		}
		remaining = remaining[openIdx+len("<think>"):]
		closeIdx := strings.Index(remaining, "</think>")
		if closeIdx == -1 {
			state.InThinkingTag = true
			appendReasoning(remaining)
			remaining = ""
			break
		}
		appendReasoning(remaining[:closeIdx])
		remaining = strings.TrimLeft(remaining[closeIdx+len("</think>"):], "\n")
	}

	if len(items) == 0 {
		items = append(items, responsesSSEItem{
			eventName: eventName,
			payload:   cloneChatChunk(template, map[string]any{"content": content}, nil),
		})
	}
	if finishReason != nil && !hasToolCalls {
		last := &items[len(items)-1]
		last.payload = cloneChatChunk(last.payload, extractChatDelta(last.payload), finishReason)
	}
	return items
}

func fixChatChunkPayload(payload map[string]any, clientModel string) map[string]any {
	if clientModel != "" {
		payload["model"] = clientModel
	}
	if choices, ok := payload["choices"].([]any); ok {
		for _, choiceValue := range choices {
			choice, ok := choiceValue.(map[string]any)
			if !ok {
				continue
			}
			fixChatStreamChoice(choice)
		}
	}
	return payload
}

func fixChatStreamChoice(choice map[string]any) {
	delta, ok := choice["delta"].(map[string]any)
	if !ok {
		rewriteChatFinishReason(choice)
		return
	}

	promoteChatReasoningField(delta)
	convertLegacyStreamFunctionCall(delta, choice)
	sanitizeToolCallDeltas(delta)
	ensureStreamToolCalls(delta, choice)
	rewriteChatFinishReason(choice)
}

func convertLegacyStreamFunctionCall(delta map[string]any, choice map[string]any) {
	if _, ok := delta["tool_calls"]; ok {
		return
	}
	functionCall, ok := delta["function_call"].(map[string]any)
	if !ok {
		return
	}

	toolCall := map[string]any{
		"index":    0,
		"type":     "function",
		"function": map[string]any{},
	}
	functionMap := toolCall["function"].(map[string]any)
	if name := messagesStringValue(functionCall["name"]); name != "" {
		functionMap["name"] = name
	}
	if arguments := messagesStringValue(functionCall["arguments"]); arguments != "" {
		functionMap["arguments"] = arguments
	}
	if toolCallHasResolvableIdentity(toolCall) {
		if choice["finish_reason"] == "function_call" {
			toolCall["id"] = "call_" + uuid.NewString()
		}
		delta["tool_calls"] = []any{toolCall}
	}
	delete(delta, "function_call")
	rewriteChatFinishReason(choice)
}

func sanitizeToolCallDeltas(delta map[string]any) {
	rawToolCalls, ok := delta["tool_calls"].([]any)
	if !ok || len(rawToolCalls) == 0 {
		return
	}

	normalized := make([]any, 0, len(rawToolCalls))
	for _, rawToolCall := range rawToolCalls {
		toolCall, ok := rawToolCall.(map[string]any)
		if !ok {
			normalized = append(normalized, rawToolCall)
			continue
		}
		cloned := cloneMessagesJSONObject(toolCall)
		if strings.TrimSpace(messagesStringValue(cloned["id"])) == "" {
			delete(cloned, "id")
		}
		if strings.TrimSpace(messagesStringValue(cloned["type"])) == "" {
			delete(cloned, "type")
		}
		function, _ := cloned["function"].(map[string]any)
		if function != nil {
			cleanFunction := cloneMessagesJSONObject(function)
			if messagesStringValue(cleanFunction["name"]) == "" {
				delete(cleanFunction, "name")
			}
			hasOnlyEmptyArguments := len(cleanFunction) == 1 &&
				messagesStringValue(cleanFunction["arguments"]) == ""
			if len(cleanFunction) == 0 {
				delete(cloned, "function")
			} else if hasOnlyEmptyArguments && !toolCallHasResolvableIdentity(cloned) {
				delete(cloned, "function")
			} else {
				cloned["function"] = cleanFunction
			}
		}
		if _, hasIndex := cloned["index"]; hasIndex && len(cloned) == 1 {
			continue
		}
		normalized = append(normalized, cloned)
	}
	if len(normalized) == 0 {
		delete(delta, "tool_calls")
		return
	}
	delta["tool_calls"] = normalized
}

func ensureStreamToolCalls(delta map[string]any, choice map[string]any) {
	rawToolCalls, ok := delta["tool_calls"].([]any)
	if !ok || len(rawToolCalls) == 0 {
		return
	}

	terminal := choice["finish_reason"] == "tool_calls"
	for _, rawToolCall := range rawToolCalls {
		toolCall, ok := rawToolCall.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := toolCall["index"]; !ok {
			toolCall["index"] = 0
		}
		if toolCallHasResolvableIdentity(toolCall) && messagesStringValue(toolCall["type"]) == "" {
			toolCall["type"] = "function"
		}
		if terminal && messagesStringValue(toolCall["id"]) == "" && toolCallHasResolvableIdentity(toolCall) {
			toolCall["id"] = "call_" + uuid.NewString()
		}
	}
}

func rewriteChatFinishReason(choice map[string]any) {
	if choice == nil {
		return
	}
	if messagesStringValue(choice["finish_reason"]) == "function_call" {
		choice["finish_reason"] = "tool_calls"
	}
}

func promoteChatReasoningField(container map[string]any) {
	if container == nil {
		return
	}
	if _, ok := container["reasoning_content"]; ok {
		return
	}
	if value, ok := container["reasoningContent"]; ok {
		container["reasoning_content"] = value
		delete(container, "reasoningContent")
	}
}

func cloneChatChunk(template map[string]any, delta map[string]any, finishReason any) map[string]any {
	cloned := cloneMessagesJSONObject(template)
	choices, _ := cloned["choices"].([]any)
	if len(choices) == 0 {
		return cloned
	}
	firstChoice, ok := choices[0].(map[string]any)
	if !ok {
		return cloned
	}
	clonedChoice := cloneMessagesJSONObject(firstChoice)
	clonedChoice["delta"] = delta
	clonedChoice["finish_reason"] = finishReason
	cloned["choices"] = []any{clonedChoice}
	return cloned
}

func extractChatDelta(payload map[string]any) map[string]any {
	choices, _ := payload["choices"].([]any)
	if len(choices) == 0 {
		return map[string]any{}
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	if delta == nil {
		return map[string]any{}
	}
	return delta
}

func toolCallHasResolvableIdentity(toolCall map[string]any) bool {
	if toolCall == nil {
		return false
	}
	if strings.TrimSpace(messagesStringValue(toolCall["id"])) != "" {
		return true
	}
	functionData, _ := toolCall["function"].(map[string]any)
	return functionData != nil && strings.TrimSpace(messagesStringValue(functionData["name"])) != ""
}
