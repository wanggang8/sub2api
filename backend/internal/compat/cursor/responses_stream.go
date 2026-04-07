package cursor

import (
	"bytes"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type ResponsesStreamState struct {
	createdEmitted   bool
	completedEmitted bool
	responseID       string
	reasoningID      string
	reasoningBuf     string
	reasoningOn      bool
	messageID        string
	messageText      string
	messageOn        bool
	tools            map[int]*responsesToolState
	output           []map[string]any
}

type responsesToolState struct {
	ID        string
	CallID    string
	Name      string
	Arguments string
	Active    bool
}

type responsesSSEItem struct {
	eventName string
	payload   map[string]any
}

func NewResponsesStreamState() *ResponsesStreamState {
	return &ResponsesStreamState{
		tools:  make(map[int]*responsesToolState),
		output: make([]map[string]any, 0),
	}
}

func PatchResponsesStreamChunk(chunk []byte, clientModel string, state *ResponsesStreamState) ([]byte, error) {
	if len(chunk) == 0 {
		return nil, nil
	}
	if state == nil {
		state = NewResponsesStreamState()
	}

	var output bytes.Buffer
	eventName, data, ok := parseMessagesSSEChunk(chunk)
	if !ok {
		output.Write(chunk)
		return output.Bytes(), nil
	}
	if data == "[DONE]" {
		return output.Bytes(), nil
	}

	payload, ok := decodeMessagesJSONObject([]byte(data))
	if !ok {
		output.Write(chunk)
		return output.Bytes(), nil
	}
	if eventName == "" {
		eventName = messagesStringValue(payload["type"])
	}
	if eventName == "response.created" && state.createdEmitted {
		return output.Bytes(), nil
	}

	for _, item := range formatResponsesStreamEventsWithState(eventName, payload, clientModel, state) {
		writeMessagesSSEChunk(&output, item.eventName, item.payload)
	}
	return output.Bytes(), nil
}

func formatResponsesStreamEventsWithState(eventName string, payload map[string]any, clientModel string, state *ResponsesStreamState) []responsesSSEItem {
	if eventName == "" {
		eventName = messagesStringValue(payload["type"])
	}
	if eventName == "" {
		return nil
	}
	if state == nil {
		ev, rewritten, ok := formatResponsesStreamEvent(eventName, payload, clientModel)
		if !ok {
			return nil
		}
		return []responsesSSEItem{{eventName: ev, payload: rewritten}}
	}
	if response, ok := payload["response"].(map[string]any); ok {
		state.responseID = responsesFirstNonEmptyString(state.responseID, messagesStringValue(response["id"]))
	}

	prefixEvents := make([]responsesSSEItem, 0)
	switch eventName {
	case "response.created":
		if response, ok := payload["response"].(map[string]any); ok {
			state.responseID = responsesFirstNonEmptyString(messagesStringValue(response["id"]), state.responseID)
			payload = cloneMessagesJSONObject(payload)
			rewritten := cloneMessagesJSONObject(response)
			if rewritten["id"] == nil && state.responseID != "" {
				rewritten["id"] = state.responseID
			}
			if rewritten["object"] == nil {
				rewritten["object"] = "response"
			}
			if rewritten["status"] == nil {
				rewritten["status"] = "in_progress"
			}
			if _, ok := rewritten["output"]; !ok {
				rewritten["output"] = []any{}
			}
			if clientModel != "" {
				rewritten["model"] = clientModel
			}
			payload["response"] = rewritten
		}
		state.createdEmitted = true
	case "response.output_item.added":
		trackResponsesAdded(state, payload)
		payload = enrichResponsesStreamItemEvent(state, payload, false)
	case "response.reasoning_summary_text.delta":
		state.reasoningBuf += messagesStringValue(payload["delta"])
		if state.reasoningID == "" {
			state.reasoningID = "rs_" + uuid.NewString()
		}
		state.reasoningOn = true
	case "response.reasoning_summary_text.done":
		payload = cloneMessagesJSONObject(payload)
		if strings.TrimSpace(messagesStringValue(payload["text"])) == "" && state.reasoningBuf != "" {
			payload["text"] = state.reasoningBuf
		}
	case "response.output_text.delta":
		state.messageText += messagesStringValue(payload["delta"])
		if state.messageID == "" {
			state.messageID = "msg_" + uuid.NewString()
		}
		state.messageOn = true
	case "response.output_text.done":
		payload = cloneMessagesJSONObject(payload)
		if strings.TrimSpace(messagesStringValue(payload["text"])) == "" && state.messageText != "" {
			payload["text"] = state.messageText
		}
	case "response.function_call_arguments.delta":
		trackResponsesToolArguments(state, payload)
	case "response.function_call_arguments.done":
		trackResponsesToolArgumentsDone(state, payload)
		payload = enrichResponsesFunctionArgumentsDone(state, payload)
	case "response.output_item.done":
		trackResponsesDone(state, payload)
		payload = enrichResponsesStreamItemEvent(state, payload, true)
	case "response.completed":
		prefixEvents = append(prefixEvents, buildResponsesFinalizeDoneEvents(state, clientModel)...)
		payload = injectResponsesCompletedOutput(state, payload, clientModel)
		state.completedEmitted = true
	case "response.incomplete", "response.failed":
		payload = injectResponsesTerminalOutput(state, payload, clientModel)
		state.completedEmitted = true
	}

	if eventName != "response.created" && !state.createdEmitted {
		state.createdEmitted = true
		prefixPayload := map[string]any{
			"id":     responsesFirstNonEmptyString(state.responseID, "resp_"+uuid.NewString()),
			"object": "response",
			"status": "in_progress",
			"output": []any{},
		}
		if clientModel != "" {
			prefixPayload["model"] = clientModel
		}
		prefixEvents = append(prefixEvents, responsesSSEItem{eventName: "response.created", payload: prefixPayload})
	}

	ev, rewritten, ok := formatResponsesStreamEvent(eventName, payload, clientModel)
	if !ok {
		return prefixEvents
	}
	prefixEvents = append(prefixEvents, responsesSSEItem{eventName: ev, payload: rewritten})
	return prefixEvents
}

func formatResponsesStreamEvent(eventName string, payload map[string]any, clientModel string) (string, map[string]any, bool) {
	if eventName == "" {
		eventName = messagesStringValue(payload["type"])
	}
	if eventName == "" {
		return "", payload, false
	}

	switch eventName {
	case "response.created", "response.completed", "response.incomplete", "response.failed":
		if response, ok := payload["response"].(map[string]any); ok {
			rewritten := cloneMessagesJSONObject(response)
			if clientModel != "" {
				rewritten["model"] = clientModel
			}
			return eventName, rewritten, true
		}
	case "response.output_item.added":
		if item, ok := payload["item"].(map[string]any); ok {
			return eventName, cloneMessagesJSONObject(item), true
		}
	case "response.output_item.done":
		if item, ok := payload["item"].(map[string]any); ok {
			rewritten := map[string]any{"item": cloneMessagesJSONObject(item)}
			if outputIndex := responsesOutputIndex(payload); outputIndex >= 0 {
				rewritten["output_index"] = outputIndex
			}
			return eventName, rewritten, true
		}
	case "response.content_part.added":
		if part, ok := payload["part"].(map[string]any); ok {
			return eventName, cloneMessagesJSONObject(part), true
		}
	case "response.content_part.done":
		return "", nil, false
	case "response.output_text.delta":
		return eventName, map[string]any{"type": "output_text", "delta": messagesStringValue(payload["delta"])}, true
	case "response.output_text.done":
		rewritten := map[string]any{"type": "output_text", "text": messagesStringValue(payload["text"])}
		if outputIndex := responsesOutputIndex(payload); outputIndex >= 0 {
			rewritten["output_index"] = outputIndex
			rewritten["content_index"] = 0
		}
		return eventName, rewritten, true
	case "response.reasoning_summary_text.delta":
		return eventName, map[string]any{"type": "summary_text", "delta": messagesStringValue(payload["delta"])}, true
	case "response.reasoning_summary_text.done":
		rewritten := map[string]any{"type": "summary_text", "text": messagesStringValue(payload["text"])}
		if outputIndex := responsesOutputIndex(payload); outputIndex >= 0 {
			rewritten["output_index"] = outputIndex
			rewritten["summary_index"] = 0
		}
		return eventName, rewritten, true
	case "response.function_call_arguments.delta":
		return eventName, map[string]any{"type": "function_call", "delta": messagesStringValue(payload["delta"])}, true
	case "response.function_call_arguments.done":
		rewritten := map[string]any{"type": "function_call", "arguments": messagesStringValue(payload["arguments"])}
		if callID := messagesStringValue(payload["call_id"]); callID != "" {
			rewritten["call_id"] = callID
		}
		if outputIndex := responsesOutputIndex(payload); outputIndex >= 0 {
			rewritten["output_index"] = outputIndex
		}
		return eventName, rewritten, true
	}

	rewritten := cloneMessagesJSONObject(payload)
	if clientModel != "" {
		if _, ok := rewritten["model"]; ok {
			rewritten["model"] = clientModel
		}
	}
	return eventName, rewritten, true
}

func trackResponsesAdded(state *ResponsesStreamState, payload map[string]any) {
	if state == nil {
		return
	}
	item := responsesPayloadItem(payload)
	switch messagesStringValue(item["type"]) {
	case "reasoning":
		state.reasoningID = responsesFirstNonEmptyString(messagesStringValue(item["id"]), state.reasoningID, "rs_"+uuid.NewString())
		state.reasoningOn = true
		if summary := extractResponsesReasoningSummary(item); summary != "" {
			state.reasoningBuf = summary
		}
	case "message":
		state.messageID = responsesFirstNonEmptyString(messagesStringValue(item["id"]), state.messageID, "msg_"+uuid.NewString())
		state.messageOn = true
		if text := extractResponsesMessageText(item); text != "" {
			state.messageText = text
		}
	case "function_call":
		tool, index := resolveResponsesToolForPayload(state, payload, true)
		if tool == nil {
			return
		}
		if tool.ID == "" {
			tool.ID = responsesFirstNonEmptyString(messagesStringValue(item["id"]), "fc_"+uuid.NewString())
		}
		if tool.CallID == "" {
			tool.CallID = responsesFirstNonEmptyString(messagesStringValue(item["call_id"]), newResponsesToolCallID())
		}
		if tool.Name == "" {
			tool.Name = messagesStringValue(item["name"])
		}
		if tool.Arguments == "" {
			tool.Arguments = messagesStringValue(item["arguments"])
		}
		tool.Active = true
		if state.tools[index] == nil {
			state.tools[index] = tool
		}
	}
}

func trackResponsesToolArguments(state *ResponsesStreamState, payload map[string]any) {
	delta := messagesStringValue(payload["delta"])
	if state == nil || delta == "" {
		return
	}
	tool, _ := resolveResponsesToolForPayload(state, payload, true)
	if tool == nil {
		return
	}
	tool.Arguments += delta
	tool.Active = true
}

func trackResponsesToolArgumentsDone(state *ResponsesStreamState, payload map[string]any) {
	arguments := messagesStringValue(payload["arguments"])
	if state == nil || arguments == "" {
		return
	}
	tool, _ := resolveResponsesToolForPayload(state, payload, true)
	if tool == nil {
		return
	}
	tool.Arguments = arguments
	tool.Active = true
}

func trackResponsesDone(state *ResponsesStreamState, payload map[string]any) {
	if state == nil {
		return
	}
	item := responsesPayloadItem(payload)
	switch messagesStringValue(item["type"]) {
	case "reasoning":
		outputItem := cloneMessagesJSONObject(item)
		if len(outputItem) == 0 {
			outputItem = buildResponsesReasoningItem(state)
		}
		if _, ok := outputItem["summary"]; !ok {
			outputItem["summary"] = []any{map[string]any{"type": "summary_text", "text": state.reasoningBuf}}
		}
		appendResponsesOutput(state, outputItem)
		state.reasoningOn = false
	case "message":
		outputItem := cloneMessagesJSONObject(item)
		if len(outputItem) == 0 {
			outputItem = buildResponsesMessageItem(state)
		}
		if _, ok := outputItem["content"]; !ok {
			outputItem["content"] = []any{map[string]any{"type": "output_text", "text": state.messageText}}
		}
		appendResponsesOutput(state, outputItem)
		state.messageOn = false
	case "function_call":
		outputItem, tool := buildResponsesToolItemFromPayload(state, payload)
		appendResponsesOutput(state, outputItem)
		if tool != nil {
			tool.Active = false
		}
	}
}

func injectResponsesCompletedOutput(state *ResponsesStreamState, payload map[string]any, clientModel string) map[string]any {
	if state == nil {
		return payload
	}
	if state.reasoningOn && state.reasoningBuf != "" {
		appendResponsesOutput(state, buildResponsesReasoningItem(state))
		state.reasoningOn = false
	}
	if state.messageOn && state.messageText != "" {
		appendResponsesOutput(state, buildResponsesMessageItem(state))
		state.messageOn = false
	}
	for _, index := range sortedResponsesToolIndexes(state) {
		tool := state.tools[index]
		if tool == nil || !tool.Active {
			continue
		}
		appendResponsesOutput(state, buildResponsesToolItem(tool))
		tool.Active = false
	}

	return injectResponsesOutput(state, payload, clientModel)
}

func injectResponsesTerminalOutput(state *ResponsesStreamState, payload map[string]any, clientModel string) map[string]any {
	if state == nil {
		return payload
	}
	if state.reasoningOn && state.reasoningBuf != "" {
		appendResponsesOutput(state, buildResponsesReasoningItem(state))
		state.reasoningOn = false
	}
	if state.messageOn && state.messageText != "" {
		appendResponsesOutput(state, buildResponsesMessagePartialItem(state))
		state.messageOn = false
	}
	for _, index := range sortedResponsesToolIndexes(state) {
		tool := state.tools[index]
		if tool == nil || !tool.Active {
			continue
		}
		appendResponsesOutput(state, buildResponsesToolPartialItem(tool))
		tool.Active = false
	}

	return injectResponsesOutput(state, payload, clientModel)
}

func injectResponsesOutput(state *ResponsesStreamState, payload map[string]any, clientModel string) map[string]any {
	if state == nil {
		return payload
	}
	rewritten := cloneMessagesJSONObject(payload)
	response, ok := payload["response"].(map[string]any)
	if !ok {
		response = map[string]any{}
	}
	rewrittenResponse := cloneMessagesJSONObject(response)
	if rewrittenResponse["id"] == nil && state.responseID != "" {
		rewrittenResponse["id"] = state.responseID
	}
	if clientModel != "" {
		rewrittenResponse["model"] = clientModel
	}
	output, hasOutput := rewrittenResponse["output"].([]any)
	if !hasOutput || len(output) == 0 {
		rewrittenResponse["output"] = responsesOutputAsInterfaces(state)
	}
	rewritten["response"] = rewrittenResponse
	return rewritten
}

func buildResponsesFinalizeDoneEvents(state *ResponsesStreamState, clientModel string) []responsesSSEItem {
	if state == nil {
		return nil
	}
	events := make([]responsesSSEItem, 0)

	if state.reasoningOn && state.reasoningBuf != "" {
		item := buildResponsesReasoningItem(state)
		appendResponsesOutput(state, item)
		state.reasoningOn = false
		outputIndex := len(state.output) - 1

		ev1, p1, ok := formatResponsesStreamEvent("response.reasoning_summary_text.done", map[string]any{
			"type":         "response.reasoning_summary_text.done",
			"output_index": outputIndex,
			"summary_index": 0,
			"text":         state.reasoningBuf,
		}, clientModel)
		if ok {
			events = append(events, responsesSSEItem{eventName: ev1, payload: p1})
		}
		ev2, p2, ok := formatResponsesStreamEvent("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": outputIndex,
			"item":         item,
		}, clientModel)
		if ok {
			events = append(events, responsesSSEItem{eventName: ev2, payload: p2})
		}
	}

	if state.messageOn && state.messageText != "" {
		item := buildResponsesMessageItem(state)
		appendResponsesOutput(state, item)
		state.messageOn = false
		outputIndex := len(state.output) - 1

		ev1, p1, ok := formatResponsesStreamEvent("response.output_text.done", map[string]any{
			"type":          "response.output_text.done",
			"output_index":  outputIndex,
			"content_index": 0,
			"text":          state.messageText,
		}, clientModel)
		if ok {
			events = append(events, responsesSSEItem{eventName: ev1, payload: p1})
		}
		ev2, p2, ok := formatResponsesStreamEvent("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": outputIndex,
			"item":         item,
		}, clientModel)
		if ok {
			events = append(events, responsesSSEItem{eventName: ev2, payload: p2})
		}
	}

	for _, index := range sortedResponsesToolIndexes(state) {
		tool := state.tools[index]
		if tool == nil || !tool.Active {
			continue
		}
		item := buildResponsesToolItem(tool)
		appendResponsesOutput(state, item)
		tool.Active = false
		outputIndex := index
		if outputIndex < 0 {
			outputIndex = len(state.output) - 1
		}

		ev1, p1, ok := formatResponsesStreamEvent("response.function_call_arguments.done", map[string]any{
			"type":         "response.function_call_arguments.done",
			"output_index": outputIndex,
			"arguments":    tool.Arguments,
			"call_id":      tool.CallID,
		}, clientModel)
		if ok {
			events = append(events, responsesSSEItem{eventName: ev1, payload: p1})
		}
		ev2, p2, ok := formatResponsesStreamEvent("response.output_item.done", map[string]any{
			"type":         "response.output_item.done",
			"output_index": outputIndex,
			"item":         item,
		}, clientModel)
		if ok {
			events = append(events, responsesSSEItem{eventName: ev2, payload: p2})
		}
	}

	return events
}

func buildResponsesReasoningItem(state *ResponsesStreamState) map[string]any {
	return map[string]any{
		"type": "reasoning",
		"id":   responsesFirstNonEmptyString(state.reasoningID, "rs_"+uuid.NewString()),
		"summary": []any{
			map[string]any{"type": "summary_text", "text": state.reasoningBuf},
		},
	}
}

func buildResponsesReasoningAddedItem(state *ResponsesStreamState) map[string]any {
	return map[string]any{
		"type":    "reasoning",
		"id":      responsesFirstNonEmptyString(state.reasoningID, "rs_"+uuid.NewString()),
		"summary": []any{},
	}
}

func buildResponsesMessageAddedItem(state *ResponsesStreamState) map[string]any {
	return map[string]any{
		"type":    "message",
		"id":      responsesFirstNonEmptyString(state.messageID, "msg_"+uuid.NewString()),
		"status":  "in_progress",
		"role":    "assistant",
		"content": []any{},
	}
}

func buildResponsesMessageItem(state *ResponsesStreamState) map[string]any {
	return map[string]any{
		"type":   "message",
		"id":     responsesFirstNonEmptyString(state.messageID, "msg_"+uuid.NewString()),
		"status": "completed",
		"role":   "assistant",
		"content": []any{
			map[string]any{"type": "output_text", "text": state.messageText},
		},
	}
}

func buildResponsesMessagePartialItem(state *ResponsesStreamState) map[string]any {
	return map[string]any{
		"type":   "message",
		"id":     responsesFirstNonEmptyString(state.messageID, "msg_"+uuid.NewString()),
		"status": "in_progress",
		"role":   "assistant",
		"content": []any{
			map[string]any{"type": "output_text", "text": state.messageText},
		},
	}
}

func enrichResponsesStreamItemEvent(state *ResponsesStreamState, payload map[string]any, done bool) map[string]any {
	if state == nil {
		return payload
	}
	rewritten := cloneMessagesJSONObject(payload)
	item := responsesPayloadItem(payload)
	itemType := messagesStringValue(item["type"])
	var enriched map[string]any

	switch itemType {
	case "reasoning":
		if done {
			enriched = buildResponsesReasoningItem(state)
		} else {
			enriched = buildResponsesReasoningAddedItem(state)
		}
	case "message":
		if done {
			enriched = buildResponsesMessageItem(state)
		} else {
			enriched = buildResponsesMessageAddedItem(state)
		}
	case "function_call":
		enriched, _ = buildResponsesToolItemFromPayload(state, payload)
		if !done {
			enriched["status"] = "in_progress"
			if _, ok := enriched["arguments"]; !ok {
				enriched["arguments"] = ""
			}
		}
	default:
		return payload
	}

	for key, value := range item {
		enriched[key] = value
	}
	rewritten["item"] = enriched
	return rewritten
}

func enrichResponsesFunctionArgumentsDone(state *ResponsesStreamState, payload map[string]any) map[string]any {
	if state == nil {
		return payload
	}
	rewritten := cloneMessagesJSONObject(payload)
	if strings.TrimSpace(messagesStringValue(rewritten["arguments"])) != "" {
		if _, ok := rewritten["type"]; !ok {
			rewritten["type"] = "function_call"
		}
		return rewritten
	}
	tool, _ := resolveResponsesToolForPayload(state, rewritten, false)
	if tool == nil || strings.TrimSpace(tool.Arguments) == "" {
		return rewritten
	}
	rewritten["arguments"] = tool.Arguments
	if _, ok := rewritten["type"]; !ok {
		rewritten["type"] = "function_call"
	}
	return rewritten
}

func buildResponsesToolItem(tool *responsesToolState) map[string]any {
	if tool == nil {
		return map[string]any{}
	}
	return map[string]any{
		"type":      "function_call",
		"id":        responsesFirstNonEmptyString(tool.ID, "fc_"+uuid.NewString()),
		"status":    "completed",
		"call_id":   responsesFirstNonEmptyString(tool.CallID, newResponsesToolCallID()),
		"name":      tool.Name,
		"arguments": tool.Arguments,
	}
}

func buildResponsesToolPartialItem(tool *responsesToolState) map[string]any {
	if tool == nil {
		return map[string]any{}
	}
	return map[string]any{
		"type":      "function_call",
		"id":        responsesFirstNonEmptyString(tool.ID, "fc_"+uuid.NewString()),
		"status":    "in_progress",
		"call_id":   responsesFirstNonEmptyString(tool.CallID, newResponsesToolCallID()),
		"name":      tool.Name,
		"arguments": tool.Arguments,
	}
}

func buildResponsesToolItemFromPayload(state *ResponsesStreamState, payload map[string]any) (map[string]any, *responsesToolState) {
	item := responsesPayloadItem(payload)
	tool, index := resolveResponsesToolForPayload(state, payload, true)
	if tool == nil {
		tool = &responsesToolState{
			ID:        responsesFirstNonEmptyString(messagesStringValue(item["id"]), responsesPayloadToolID(payload), "fc_"+uuid.NewString()),
			CallID:    responsesFirstNonEmptyString(messagesStringValue(item["call_id"]), responsesPayloadCallID(payload), newResponsesToolCallID()),
			Name:      messagesStringValue(item["name"]),
			Arguments: messagesStringValue(item["arguments"]),
			Active:    true,
		}
		if state != nil {
			if state.tools == nil {
				state.tools = make(map[int]*responsesToolState)
			}
			if index < 0 {
				index = nextResponsesToolIndex(state)
			}
			state.tools[index] = tool
		}
	}
	if tool.ID == "" {
		tool.ID = responsesFirstNonEmptyString(messagesStringValue(item["id"]), responsesPayloadToolID(payload), "fc_"+uuid.NewString())
	}
	if tool.CallID == "" {
		tool.CallID = responsesFirstNonEmptyString(messagesStringValue(item["call_id"]), responsesPayloadCallID(payload), newResponsesToolCallID())
	}
	if tool.Name == "" {
		tool.Name = messagesStringValue(item["name"])
	}
	if tool.Arguments == "" {
		tool.Arguments = messagesStringValue(item["arguments"])
	}

	outputItem := cloneMessagesJSONObject(item)
	if _, ok := outputItem["status"]; !ok {
		outputItem["status"] = "completed"
	}
	if _, ok := outputItem["id"]; !ok {
		outputItem["id"] = tool.ID
	}
	if _, ok := outputItem["call_id"]; !ok {
		outputItem["call_id"] = tool.CallID
	}
	if _, ok := outputItem["name"]; !ok {
		outputItem["name"] = tool.Name
	}
	if _, ok := outputItem["arguments"]; !ok {
		outputItem["arguments"] = tool.Arguments
	}
	if _, ok := outputItem["type"]; !ok {
		outputItem["type"] = "function_call"
	}
	return outputItem, tool
}

func responsesPayloadItem(payload map[string]any) map[string]any {
	if nested, ok := payload["item"].(map[string]any); ok {
		return nested
	}
	return payload
}

func responsesPayloadCallID(payload map[string]any) string {
	item := responsesPayloadItem(payload)
	return responsesFirstNonEmptyString(
		messagesStringValue(item["call_id"]),
		messagesStringValue(item["callId"]),
		messagesStringValue(item["callID"]),
		messagesStringValue(payload["call_id"]),
		messagesStringValue(payload["callId"]),
		messagesStringValue(payload["callID"]),
	)
}

func responsesPayloadToolID(payload map[string]any) string {
	item := responsesPayloadItem(payload)
	return responsesFirstNonEmptyString(
		messagesStringValue(item["id"]),
		messagesStringValue(payload["item_id"]),
		messagesStringValue(payload["itemId"]),
		messagesStringValue(payload["id"]),
	)
}

func resolveResponsesToolForPayload(state *ResponsesStreamState, payload map[string]any, allowCreate bool) (*responsesToolState, int) {
	if state == nil {
		return nil, -1
	}
	if state.tools == nil && allowCreate {
		state.tools = make(map[int]*responsesToolState)
	}

	index := responsesOutputIndex(payload)
	callID := responsesPayloadCallID(payload)
	toolID := responsesPayloadToolID(payload)

	if index >= 0 {
		if tool := state.tools[index]; tool != nil {
			return tool, index
		}
	}
	if tool, idx := findResponsesToolByCallID(state, callID); tool != nil {
		return tool, idx
	}
	if tool, idx := findResponsesToolByID(state, toolID); tool != nil {
		return tool, idx
	}
	if index < 0 && callID == "" && toolID == "" && shouldFallbackToUniqueActiveResponsesTool(payload) {
		if tool, idx := uniqueActiveResponsesTool(state); tool != nil {
			return tool, idx
		}
	}
	if !allowCreate {
		return nil, -1
	}
	if index < 0 && callID == "" && toolID == "" {
		return nil, -1
	}

	item := responsesPayloadItem(payload)
	tool := &responsesToolState{
		ID:        toolID,
		CallID:    callID,
		Name:      messagesStringValue(item["name"]),
		Arguments: messagesStringValue(item["arguments"]),
		Active:    true,
	}
	if state.tools == nil {
		state.tools = make(map[int]*responsesToolState)
	}
	if index < 0 {
		index = nextResponsesToolIndex(state)
	}
	if current := state.tools[index]; current != nil && current != tool {
		mergeResponsesToolState(tool, current)
	}
	state.tools[index] = tool
	return tool, index
}

func shouldFallbackToUniqueActiveResponsesTool(payload map[string]any) bool {
	switch messagesStringValue(payload["type"]) {
	case "response.function_call_arguments.delta", "response.function_call_arguments.done":
		return true
	case "response.output_item.done":
		return messagesStringValue(responsesPayloadItem(payload)["type"]) == "function_call"
	default:
		return false
	}
}

func uniqueActiveResponsesTool(state *ResponsesStreamState) (*responsesToolState, int) {
	if state == nil {
		return nil, -1
	}
	var found *responsesToolState
	foundIndex := -1
	for index, tool := range state.tools {
		if tool == nil || !tool.Active {
			continue
		}
		if found != nil {
			return nil, -1
		}
		found = tool
		foundIndex = index
	}
	return found, foundIndex
}

func findResponsesToolByCallID(state *ResponsesStreamState, callID string) (*responsesToolState, int) {
	if state == nil || callID == "" {
		return nil, -1
	}
	for index, tool := range state.tools {
		if tool != nil && tool.CallID == callID {
			return tool, index
		}
	}
	return nil, -1
}

func findResponsesToolByID(state *ResponsesStreamState, id string) (*responsesToolState, int) {
	if state == nil || id == "" {
		return nil, -1
	}
	for index, tool := range state.tools {
		if tool != nil && tool.ID == id {
			return tool, index
		}
	}
	return nil, -1
}

func mergeResponsesToolState(dst, src *responsesToolState) {
	if dst == nil || src == nil || dst == src {
		return
	}
	if dst.ID == "" {
		dst.ID = src.ID
	}
	if dst.CallID == "" {
		dst.CallID = src.CallID
	}
	if dst.Name == "" {
		dst.Name = src.Name
	}
	if src.Arguments != "" {
		switch {
		case dst.Arguments == "":
			dst.Arguments = src.Arguments
		case strings.HasPrefix(src.Arguments, dst.Arguments):
			dst.Arguments = src.Arguments
		}
	}
	if src.Active {
		dst.Active = true
	}
}

func nextResponsesToolIndex(state *ResponsesStreamState) int {
	for index := 0; ; index++ {
		if state == nil || state.tools[index] == nil {
			return index
		}
	}
}

func appendResponsesOutput(state *ResponsesStreamState, item map[string]any) {
	if state == nil || len(item) == 0 {
		return
	}
	id := messagesStringValue(item["id"])
	for index, existing := range state.output {
		if id != "" && id == messagesStringValue(existing["id"]) {
			state.output[index] = cloneMessagesJSONObject(item)
			return
		}
	}
	state.output = append(state.output, cloneMessagesJSONObject(item))
}

func responsesOutputAsInterfaces(state *ResponsesStreamState) []any {
	if state == nil || len(state.output) == 0 {
		return []any{}
	}
	output := make([]any, 0, len(state.output))
	for _, item := range state.output {
		output = append(output, cloneMessagesJSONObject(item))
	}
	return output
}

func extractResponsesMessageText(item map[string]any) string {
	content, ok := item["content"].([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, partValue := range content {
		part, ok := partValue.(map[string]any)
		if !ok || messagesStringValue(part["type"]) != "output_text" {
			continue
		}
		builder.WriteString(messagesStringValue(part["text"]))
	}
	return builder.String()
}

func extractResponsesReasoningSummary(item map[string]any) string {
	summary, ok := item["summary"].([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, partValue := range summary {
		part, ok := partValue.(map[string]any)
		if !ok {
			continue
		}
		builder.WriteString(messagesStringValue(part["text"]))
	}
	return builder.String()
}

func responsesOutputIndex(payload map[string]any) int {
	switch index := payload["output_index"].(type) {
	case float64:
		return int(index)
	case int:
		return index
	case int64:
		return int(index)
	default:
		return -1
	}
}

func sortedResponsesToolIndexes(state *ResponsesStreamState) []int {
	if state == nil || len(state.tools) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(state.tools))
	for index := range state.tools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}

func newResponsesToolCallID() string {
	return "call_" + uuid.NewString()
}

func responsesFirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
