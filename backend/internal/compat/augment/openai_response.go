package augment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type OpenAIResponsesConvertOptions struct {
	ToolMetaByName      map[string]ToolMetadata
	SupportToolUseStart bool
}

type OpenAIResponsesStreamPatcher struct {
	options             OpenAIResponsesConvertOptions
	nextNodeID          int
	sawToolUse          bool
	stopReasonSeen      bool
	stopReason          int
	usage               map[string]interface{}
	toolCallsByOutput   map[int]*responsesToolCall
	textTracker         *responsesTextTracker
	thinkingSummary     string
	thinkingEncrypted   string
	endedWithTerminal   bool
	renderedJSONPayload bool
}

type responsesTextTracker struct {
	fullTextByOutputIndex map[int]string
}

func newResponsesTextTracker() *responsesTextTracker {
	return &responsesTextTracker{fullTextByOutputIndex: make(map[int]string)}
}

func (t *responsesTextTracker) pushDelta(outputIndex int, delta string) {
	if t == nil || delta == "" {
		return
	}
	t.fullTextByOutputIndex[normalizeOutputIndex(outputIndex)] += delta
}

func (t *responsesTextTracker) applyFinalText(outputIndex int, fullText string) string {
	if t == nil || fullText == "" {
		return ""
	}
	idx := normalizeOutputIndex(outputIndex)
	prev := t.fullTextByOutputIndex[idx]
	if prev == "" {
		t.fullTextByOutputIndex[idx] = fullText
		return fullText
	}
	if strings.HasPrefix(fullText, prev) {
		t.fullTextByOutputIndex[idx] = fullText
		return fullText[len(prev):]
	}
	return ""
}

func NewOpenAIResponsesStreamPatcher(options OpenAIResponsesConvertOptions) *OpenAIResponsesStreamPatcher {
	return &OpenAIResponsesStreamPatcher{
		options:           options,
		nextNodeID:        1,
		stopReason:        augmentStopReasonEndTurn,
		usage:             map[string]interface{}{},
		toolCallsByOutput: map[int]*responsesToolCall{},
		textTracker:       newResponsesTextTracker(),
	}
}

func StreamConvertOpenAIResponsesSSEToNDJSON(r io.Reader, w io.Writer, options OpenAIResponsesConvertOptions) (inputTokens, outputTokens int, usageEmitted bool, err error) {
	patcher := NewOpenAIResponsesStreamPatcher(options)
	err = processSSEEvents(r, func(eventType string, data string) error {
		return patcher.patchEvent(eventType, data, w)
	})
	if err != nil {
		return 0, 0, false, err
	}
	return patcher.finishInto(w)
}

func ConvertOpenAIResponsesJSONToNDJSON(body []byte, w io.Writer, options OpenAIResponsesConvertOptions) (inputTokens, outputTokens int, usageEmitted bool, err error) {
	obj, err := unmarshalJSONObject(body)
	if err != nil {
		return 0, 0, false, err
	}
	resp := pickResponsesObject(obj)
	if resp == nil {
		return 0, 0, false, fmt.Errorf("augment response: OpenAI upstream response is empty")
	}
	if err := checkOpenAIResponsesError(resp); err != nil {
		return 0, 0, false, err
	}

	nextNodeID := 1
	text := extractResponsesOutputText(resp)
	if text != "" {
		writeChunkLine(w, newBaseChunk(text))
	}

	output := responsesOutputItems(resp)
	reasoningSummary, reasoningEncrypted := extractResponsesReasoning(output)
	if reasoningSummary != "" || reasoningEncrypted != "" {
		emitOpenAIThinkingChunk(w, reasoningSummary, reasoningEncrypted, &nextNodeID)
	}

	sawToolUse := false
	for _, toolCall := range extractResponsesToolCalls(output) {
		if emitToolUseChunksWithMetadata(w, toolCall.CallID, toolCall.Name, toolCall.Arguments, options.lookupToolMeta(toolCall.Name), options.SupportToolUseStart, &nextNodeID) {
			sawToolUse = true
		}
	}

	usage := extractResponsesUsage(resp)
	usageEmitted = emitTokenUsageChunk(w, usage, &nextNodeID)
	inputTokens = usageIntValue(usage["input_tokens"])
	outputTokens = usageIntValue(usage["output_tokens"])

	stopReasonSeen, stopReason := mapResponsesStopReason(resp)
	emitFinalStopChunk(w, stopReasonSeen, stopReason, sawToolUse, true)
	return inputTokens, outputTokens, usageEmitted, nil
}

func (p *OpenAIResponsesStreamPatcher) PatchChunk(chunk []byte) ([]byte, error) {
	var out bytes.Buffer
	eventType, data, ok := parseOpenAICompatSSEChunk(chunk)
	if !ok {
		raw := bytes.TrimSpace(chunk)
		if len(raw) == 0 {
			return nil, nil
		}
		if raw[0] != '{' && raw[0] != '[' {
			return nil, nil
		}
		if _, _, _, err := ConvertOpenAIResponsesJSONToNDJSON(raw, &out, p.options); err != nil {
			return nil, err
		}
		p.renderedJSONPayload = true
		p.endedWithTerminal = true
		return out.Bytes(), nil
	}
	if err := p.patchEvent(eventType, data, &out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (p *OpenAIResponsesStreamPatcher) Finish() ([]byte, error) {
	if p.renderedJSONPayload {
		return nil, nil
	}
	var out bytes.Buffer
	_, _, _, err := p.finishInto(&out)
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (p *OpenAIResponsesStreamPatcher) finishInto(w io.Writer) (inputTokens, outputTokens int, usageEmitted bool, err error) {
	if p == nil {
		return 0, 0, false, nil
	}
	if p.thinkingSummary != "" || p.thinkingEncrypted != "" {
		emitOpenAIThinkingChunk(w, p.thinkingSummary, p.thinkingEncrypted, &p.nextNodeID)
	}
	for _, outputIndex := range sortedToolCallIndexes(p.toolCallsByOutput) {
		toolCall := p.toolCallsByOutput[outputIndex]
		if toolCall == nil {
			continue
		}
		if emitToolUseChunksWithMetadata(w, toolCall.CallID, toolCall.Name, toolCall.Arguments, p.options.lookupToolMeta(toolCall.Name), p.options.SupportToolUseStart, &p.nextNodeID) {
			p.sawToolUse = true
		}
	}
	usageEmitted = emitTokenUsageChunk(w, p.usage, &p.nextNodeID)
	emitFinalStopChunk(w, p.stopReasonSeen, p.stopReason, p.sawToolUse, p.endedWithTerminal)
	inputTokens = usageIntValue(p.usage["input_tokens"])
	outputTokens = usageIntValue(p.usage["output_tokens"])
	return inputTokens, outputTokens, usageEmitted, nil
}

func (p *OpenAIResponsesStreamPatcher) patchEvent(eventType, data string, w io.Writer) error {
	if p == nil {
		return nil
	}
	data = strings.TrimSpace(data)
	if data == "" {
		return nil
	}
	if data == "[DONE]" {
		p.endedWithTerminal = true
		return nil
	}

	ev, err := unmarshalJSONObject([]byte(data))
	if err != nil {
		return nil
	}
	if _, ok := ev["type"].(string); !ok && strings.TrimSpace(eventType) != "" {
		ev["type"] = strings.TrimSpace(eventType)
	}

	typ := strings.TrimSpace(stringValue(ev["type"]))
	if typ == "response.error" || typ == "error" || firstMap(ev, "error") != nil {
		return fmt.Errorf("augment response: OpenAI upstream error: %s", extractResponsesErrorMessage(ev))
	}

	switch typ {
	case "response.output_item.added":
		item := firstMap(ev, "item")
		if strings.EqualFold(stringValue(item["type"]), "function_call") {
			outputIndex := normalizeOutputIndex(intValue(ev["output_index"]))
			callID := strings.TrimSpace(stringValue(item["call_id"]))
			name := strings.TrimSpace(stringValue(item["name"]))
			if callID != "" && name != "" {
				p.toolCallsByOutput[outputIndex] = &responsesToolCall{
					CallID:    callID,
					Name:      name,
					Arguments: strings.TrimSpace(stringValue(item["arguments"])),
				}
			}
		}
	case "response.function_call_arguments.delta":
		outputIndex := normalizeOutputIndex(intValue(ev["output_index"]))
		if toolCall := p.toolCallsByOutput[outputIndex]; toolCall != nil {
			toolCall.Arguments += stringValue(ev["delta"])
		}
	case "response.function_call_arguments.done":
		outputIndex := normalizeOutputIndex(intValue(ev["output_index"]))
		if toolCall := p.toolCallsByOutput[outputIndex]; toolCall != nil {
			if arguments := strings.TrimSpace(stringValue(ev["arguments"])); arguments != "" {
				toolCall.Arguments = arguments
			}
		}
	case "response.output_text.delta":
		text := stringValue(ev["delta"])
		if text != "" {
			p.textTracker.pushDelta(intValue(ev["output_index"]), text)
			writeChunkLine(w, newBaseChunk(text))
		}
	case "response.output_text.done":
		rest := p.textTracker.applyFinalText(intValue(ev["output_index"]), stringValue(ev["text"]))
		if rest != "" {
			writeChunkLine(w, newBaseChunk(rest))
		}
	case "response.reasoning_summary_part.added":
		part := firstMap(ev, "part")
		p.appendThinkingText(stringValue(part["text"]))
	case "response.reasoning_summary_text.done":
		p.appendThinkingText(stringValue(ev["text"]))
	case "response.reasoning_text.delta":
		p.appendThinkingText(stringValue(ev["delta"]))
	case "response.incomplete":
		resp := pickResponsesObject(ev)
		if resp == nil {
			resp = firstMap(ev, "response")
		}
		p.stopReasonSeen = true
		p.stopReason = mapResponsesIncompleteReason(reasonFromResponsesObject(resp))
		p.endedWithTerminal = true
		mergeUsage(p.usage, extractResponsesUsage(resp))
	case "response.failed":
		p.endedWithTerminal = true
		return fmt.Errorf("augment response: OpenAI upstream error: %s", extractResponsesErrorMessage(ev))
	case "response.completed":
		resp := pickResponsesObject(ev)
		if resp == nil {
			resp = firstMap(ev, "response")
		}
		p.endedWithTerminal = true
		if resp != nil {
			rest := p.textTracker.applyFinalText(0, stringValue(resp["output_text"]))
			if rest != "" {
				writeChunkLine(w, newBaseChunk(rest))
			}
			mergeUsage(p.usage, extractResponsesUsage(resp))
			output := responsesOutputItems(resp)
			if summary, encrypted := extractResponsesReasoning(output); summary != "" || encrypted != "" {
				if summary != "" {
					p.thinkingSummary = summary
				}
				if encrypted != "" {
					p.thinkingEncrypted = encrypted
				}
			}
			for _, toolCall := range extractResponsesToolCalls(output) {
				if hasToolCallID(p.toolCallsByOutput, toolCall.CallID) {
					continue
				}
				nextIndex := len(p.toolCallsByOutput)
				for {
					if _, ok := p.toolCallsByOutput[nextIndex]; !ok {
						break
					}
					nextIndex++
				}
				p.toolCallsByOutput[nextIndex] = &responsesToolCall{
					CallID:    toolCall.CallID,
					Name:      toolCall.Name,
					Arguments: toolCall.Arguments,
				}
			}
		}
	}

	return nil
}

func (p *OpenAIResponsesStreamPatcher) appendThinkingText(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if p.thinkingSummary == "" {
		p.thinkingSummary = text
		return
	}
	p.thinkingSummary += "\n" + text
}

func (o OpenAIResponsesConvertOptions) lookupToolMeta(name string) ToolMetadata {
	if o.ToolMetaByName == nil {
		return ToolMetadata{}
	}
	return o.ToolMetaByName[strings.TrimSpace(name)]
}

func emitOpenAIThinkingChunk(w io.Writer, summary, encryptedContent string, nextNodeID *int) {
	summary = strings.TrimSpace(summary)
	encryptedContent = strings.TrimSpace(encryptedContent)
	if summary == "" && encryptedContent == "" {
		return
	}
	node := map[string]interface{}{
		"id":      *nextNodeID,
		"type":    augmentNodeTypeThinking,
		"content": "",
		"thinking": map[string]interface{}{
			"summary": summary,
		},
	}
	if encryptedContent != "" {
		node["thinking"].(map[string]interface{})["encrypted_content"] = encryptedContent
	}
	*nextNodeID++
	chunk := newBaseChunk("")
	chunk["nodes"] = []interface{}{node}
	writeChunkLine(w, chunk)
}

func emitToolUseChunksWithMetadata(w io.Writer, toolUseID, toolName, inputJSON string, meta ToolMetadata, supportToolUseStart bool, nextNodeID *int) bool {
	if strings.TrimSpace(toolName) == "" {
		return false
	}
	if strings.TrimSpace(toolUseID) == "" {
		toolUseID = fmt.Sprintf("tool-%d", *nextNodeID)
	}
	if strings.TrimSpace(inputJSON) == "" {
		inputJSON = "{}"
	}
	toolUse := map[string]interface{}{
		"tool_name":   toolName,
		"tool_use_id": toolUseID,
		"input_json":  inputJSON,
	}
	if meta.MCPServerName != "" {
		toolUse["mcp_server_name"] = meta.MCPServerName
	}
	if meta.MCPToolName != "" {
		toolUse["mcp_tool_name"] = meta.MCPToolName
	}
	if supportToolUseStart {
		startNode := map[string]interface{}{"id": *nextNodeID, "type": augmentNodeTypeToolUseStart, "content": "", "tool_use": toolUse}
		*nextNodeID++
		startChunk := newBaseChunk("")
		startChunk["nodes"] = []interface{}{startNode}
		writeChunkLine(w, startChunk)
	}
	node := map[string]interface{}{"id": *nextNodeID, "type": augmentNodeTypeToolUse, "content": "", "tool_use": toolUse}
	*nextNodeID++
	chunk := newBaseChunk("")
	chunk["nodes"] = []interface{}{node}
	writeChunkLine(w, chunk)
	return true
}

func pickResponsesObject(obj map[string]any) map[string]any {
	if obj == nil {
		return nil
	}
	if resp := firstMap(obj, "response"); len(resp) > 0 {
		return resp
	}
	return obj
}

func responsesOutputItems(obj map[string]any) []map[string]any {
	raw, ok := obj["output"].([]any)
	if !ok {
		return nil
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if typed, ok := item.(map[string]any); ok {
			items = append(items, typed)
		}
	}
	return items
}

func extractResponsesOutputText(obj map[string]any) string {
	if obj == nil {
		return ""
	}
	if direct := strings.TrimSpace(stringValue(obj["output_text"])); direct != "" {
		return direct
	}
	if direct := strings.TrimSpace(stringValue(obj["text"])); direct != "" {
		return direct
	}

	parts := make([]string, 0)
	for _, item := range responsesOutputItems(obj) {
		itemType := strings.TrimSpace(strings.ToLower(stringValue(item["type"])))
		switch itemType {
		case "message":
			if strings.EqualFold(stringValue(item["role"]), "assistant") {
				if content, ok := item["content"].(string); ok && strings.TrimSpace(content) != "" {
					parts = append(parts, strings.TrimSpace(content))
					continue
				}
				for _, part := range contentParts(item["content"]) {
					partType := strings.TrimSpace(strings.ToLower(stringValue(part["type"])))
					if partType == "output_text" || partType == "text" {
						if text := strings.TrimSpace(stringValue(part["text"])); text != "" {
							parts = append(parts, text)
						}
					}
				}
			}
		case "output_text", "text":
			if text := strings.TrimSpace(stringValue(item["text"])); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, ""))
}

func extractResponsesReasoning(output []map[string]any) (summary string, encryptedContent string) {
	parts := make([]string, 0)
	for _, item := range output {
		if !strings.EqualFold(stringValue(item["type"]), "reasoning") {
			continue
		}
		if encrypted := strings.TrimSpace(stringValue(item["encrypted_content"])); encrypted != "" && encryptedContent == "" {
			encryptedContent = encrypted
		}
		if summaries, ok := item["summary"].([]any); ok {
			for _, entry := range summaries {
				if typed, ok := entry.(map[string]any); ok {
					if strings.EqualFold(stringValue(typed["type"]), "summary_text") {
						if text := strings.TrimSpace(stringValue(typed["text"])); text != "" {
							parts = append(parts, text)
						}
					}
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n")), encryptedContent
}

func extractResponsesToolCalls(output []map[string]any) []responsesToolCall {
	toolCalls := make([]responsesToolCall, 0)
	seen := make(map[string]struct{})
	for _, item := range output {
		if !strings.EqualFold(stringValue(item["type"]), "function_call") {
			continue
		}
		callID := strings.TrimSpace(stringValue(item["call_id"]))
		name := strings.TrimSpace(stringValue(item["name"]))
		if callID == "" || name == "" {
			continue
		}
		if _, ok := seen[callID]; ok {
			continue
		}
		seen[callID] = struct{}{}
		arguments := strings.TrimSpace(stringValue(item["arguments"]))
		if arguments == "" {
			arguments = "{}"
		}
		toolCalls = append(toolCalls, responsesToolCall{
			CallID:    callID,
			Name:      name,
			Arguments: arguments,
		})
	}
	return toolCalls
}

func extractResponsesUsage(obj map[string]any) map[string]interface{} {
	if obj == nil {
		return map[string]interface{}{}
	}
	usage := firstMap(obj, "usage")
	if len(usage) == 0 {
		return map[string]interface{}{}
	}
	out := map[string]interface{}{}
	if inputTokens, ok := usageNumber(usage["input_tokens"]); ok {
		out["input_tokens"] = inputTokens
	}
	if outputTokens, ok := usageNumber(usage["output_tokens"]); ok {
		out["output_tokens"] = outputTokens
	}
	if inputDetails := firstMap(usage, "input_tokens_details"); len(inputDetails) > 0 {
		if cachedTokens, ok := usageNumber(inputDetails["cached_tokens"]); ok {
			out["cache_read_input_tokens"] = cachedTokens
		}
	}
	if outputDetails := firstMap(usage, "output_tokens_details"); len(outputDetails) > 0 {
		if reasoningTokens, ok := usageNumber(outputDetails["reasoning_tokens"]); ok && reasoningTokens > 0 {
			out["reasoning_tokens"] = reasoningTokens
		}
	}
	return out
}

func mapResponsesStopReason(obj map[string]any) (bool, int) {
	status := strings.TrimSpace(strings.ToLower(stringValue(obj["status"])))
	if status != "incomplete" {
		return false, augmentStopReasonEndTurn
	}
	return true, mapResponsesIncompleteReason(reasonFromResponsesObject(obj))
}

func reasonFromResponsesObject(obj map[string]any) string {
	if obj == nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(stringValue(firstMap(obj, "incomplete_details")["reason"])))
}

func mapResponsesIncompleteReason(reason string) int {
	switch strings.TrimSpace(strings.ToLower(reason)) {
	case "max_output_tokens":
		return augmentStopReasonMaxTokens
	case "content_filter":
		return augmentStopReasonSafety
	default:
		return augmentStopReasonUnspecified
	}
}

func extractResponsesErrorMessage(obj map[string]any) string {
	if errObj := firstMap(obj, "error"); len(errObj) > 0 {
		if msg := strings.TrimSpace(stringValue(errObj["message"])); msg != "" {
			return msg
		}
	}
	if response := firstMap(obj, "response"); len(response) > 0 {
		if errObj := firstMap(response, "error"); len(errObj) > 0 {
			if msg := strings.TrimSpace(stringValue(errObj["message"])); msg != "" {
				return msg
			}
		}
	}
	if msg := strings.TrimSpace(stringValue(obj["message"])); msg != "" {
		return msg
	}
	return "OpenAI upstream error"
}

func checkOpenAIResponsesError(obj map[string]any) error {
	if obj == nil {
		return fmt.Errorf("augment response: OpenAI upstream response is empty")
	}
	if firstMap(obj, "error") != nil {
		return fmt.Errorf("augment response: OpenAI upstream error: %s", extractResponsesErrorMessage(obj))
	}
	if strings.EqualFold(stringValue(obj["status"]), "failed") {
		return fmt.Errorf("augment response: OpenAI upstream error: %s", extractResponsesErrorMessage(obj))
	}
	return nil
}

func contentParts(raw any) []map[string]any {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if typed, ok := item.(map[string]any); ok {
			out = append(out, typed)
		}
	}
	return out
}

func usageNumber(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func usageIntValue(value any) int {
	v, _ := usageNumber(value)
	return v
}

func normalizeOutputIndex(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func hasToolCallID(toolCalls map[int]*responsesToolCall, callID string) bool {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return false
	}
	for _, toolCall := range toolCalls {
		if toolCall != nil && strings.TrimSpace(toolCall.CallID) == callID {
			return true
		}
	}
	return false
}

func sortedToolCallIndexes(toolCalls map[int]*responsesToolCall) []int {
	if len(toolCalls) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(toolCalls))
	for index := range toolCalls {
		indexes = append(indexes, index)
	}
	for i := 0; i < len(indexes); i++ {
		for j := i + 1; j < len(indexes); j++ {
			if indexes[j] < indexes[i] {
				indexes[i], indexes[j] = indexes[j], indexes[i]
			}
		}
	}
	return indexes
}

func parseOpenAICompatSSEChunk(chunk []byte) (string, string, bool) {
	if len(chunk) == 0 {
		return "", "", false
	}
	lines := strings.Split(strings.ReplaceAll(string(chunk), "\r\n", "\n"), "\n")
	eventName := ""
	dataLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(dataLines) == 0 {
		return "", "", false
	}
	return eventName, strings.Join(dataLines, "\n"), true
}

func unmarshalJSONObject(raw []byte) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
