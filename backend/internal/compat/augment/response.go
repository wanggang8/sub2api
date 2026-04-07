package augment

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	augmentStopReasonUnspecified      = 0
	augmentStopReasonEndTurn          = 1
	augmentStopReasonMaxTokens        = 2
	augmentStopReasonToolUseRequested = 3
	augmentStopReasonSafety           = 4
	augmentStopReasonRecitation       = 5

	augmentNodeTypeToolUse      = 5
	augmentNodeTypeToolUseStart = 7
	augmentNodeTypeThinking     = 8
	augmentNodeTypeTokenUsage   = 10
)

type augmentThinkingState struct {
	summary   string
	signature string
}

func StreamConvertSSEToNDJSON(r io.Reader, w io.Writer) (inputTokens, outputTokens int, usageEmitted bool, err error) {
	nextNodeID := 1
	sawToolUse := false
	stopReasonSeen := false
	stopReason := augmentStopReasonEndTurn
	usage := map[string]interface{}{}
	thinkingStates := map[int]*augmentThinkingState{}
	var toolID, toolName string
	var toolInput strings.Builder
	toolActive := false

	err = processSSEEvents(r, func(eventType string, data string) error {
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			return nil
		}
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return nil
		}
		if _, ok := ev["type"].(string); !ok && eventType != "" {
			ev["type"] = eventType
		}
		typ := stringValue(ev["type"])
		if typ == "error" || firstMap(ev, "error") != nil {
			return fmt.Errorf("augment response: claude upstream error: %s", extractUpstreamErrorMessage(ev))
		}
		switch typ {
		case "content_block_start":
			cb := firstMap(ev, "content_block")
			if stringValue(cb["type"]) == "tool_use" {
				toolActive = true
				toolID = stringValue(cb["id"])
				toolName = stringValue(cb["name"])
				toolInput.Reset()
			}
		case "content_block_delta":
			delta := firstMap(ev, "delta")
			switch stringValue(delta["type"]) {
			case "text_delta":
				text := stringValue(delta["text"])
				if text != "" {
					writeChunkLine(w, newBaseChunk(text))
				}
			case "input_json_delta":
				if toolActive {
					toolInput.WriteString(stringValue(delta["partial_json"]))
				}
			case "thinking_delta":
				thinking := stringValue(delta["thinking"])
				if strings.TrimSpace(thinking) != "" {
					idx := intValue(ev["index"])
					state := thinkingStates[idx]
					if state == nil {
						state = &augmentThinkingState{}
						thinkingStates[idx] = state
					}
					state.summary += thinking
					emitThinkingChunk(w, state.summary, state.signature, &nextNodeID)
				}
			case "signature_delta":
				signature := stringValue(delta["signature"])
				if strings.TrimSpace(signature) != "" {
					idx := intValue(ev["index"])
					state := thinkingStates[idx]
					if state == nil {
						state = &augmentThinkingState{}
						thinkingStates[idx] = state
					}
					state.signature += signature
					emitThinkingChunk(w, state.summary, state.signature, &nextNodeID)
				}
			}
		case "content_block_stop":
			if toolActive {
				if emitToolUseChunks(w, toolID, toolName, toolInput.String(), &nextNodeID) {
					sawToolUse = true
				}
				toolActive = false
			}
		case "message_start":
			mergeUsage(usage, firstMap(ev, "usage"))
			mergeUsage(usage, firstMap(firstMap(ev, "message"), "usage"))
		case "message_delta":
			delta := firstMap(ev, "delta")
			if sr := stringValue(delta["stop_reason"]); sr != "" {
				stopReasonSeen = true
				stopReason = mapClaudeStopReason(sr)
			}
			mergeUsage(usage, firstMap(ev, "usage"))
		case "message_stop":
			mergeUsage(usage, firstMap(ev, "usage"))
		}
		return nil
	})
	if err != nil {
		return 0, 0, false, err
	}
	if emitTokenUsageChunk(w, usage, &nextNodeID) {
		usageEmitted = true
	}
	emitFinalStopChunk(w, stopReasonSeen, stopReason, sawToolUse, true)
	if v, ok := usage["input_tokens"].(int); ok {
		inputTokens = v
	}
	if v, ok := usage["output_tokens"].(int); ok {
		outputTokens = v
	}
	return inputTokens, outputTokens, usageEmitted, nil
}

func ConvertJSONToNDJSON(body []byte, w io.Writer) (inputTokens, outputTokens int, usageEmitted bool, err error) {
	var obj map[string]interface{}
	if err := json.Unmarshal(body, &obj); err != nil {
		return 0, 0, false, err
	}
	msg := obj
	if nested := firstMap(obj, "message"); len(nested) > 0 {
		msg = nested
	}
	if firstMap(msg, "error") != nil || stringValue(msg["type"]) == "error" {
		return 0, 0, false, fmt.Errorf("augment response: claude upstream error: %s", extractUpstreamErrorMessage(msg))
	}
	nextNodeID := 1
	sawToolUse := false
	stopReasonSeen := false
	stopReason := augmentStopReasonEndTurn
	if sr := stringValue(msg["stop_reason"]); sr != "" {
		stopReasonSeen = true
		stopReason = mapClaudeStopReason(sr)
	}
	for _, raw := range firstArray(msg, "content") {
		block, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		switch stringValue(block["type"]) {
		case "text":
			if text := stringValue(block["text"]); text != "" {
				writeChunkLine(w, newBaseChunk(text))
			}
		case "thinking":
			if thinking := stringValue(block["thinking"]); thinking != "" {
				emitThinkingChunk(w, thinking, stringValue(block["signature"]), &nextNodeID)
			}
		case "tool_use":
			input := "{}"
			if rawInput, ok := block["input"]; ok {
				if encoded, err := json.Marshal(rawInput); err == nil {
					input = string(encoded)
				}
			}
			if emitToolUseChunks(w, stringValue(block["id"]), stringValue(block["name"]), input, &nextNodeID) {
				sawToolUse = true
			}
		}
	}
	usage := map[string]interface{}{}
	mergeUsage(usage, firstMap(msg, "usage"))
	usageEmitted = emitTokenUsageChunk(w, usage, &nextNodeID)
	emitFinalStopChunk(w, stopReasonSeen, stopReason, sawToolUse, true)
	if v, ok := usage["input_tokens"].(int); ok {
		inputTokens = v
	}
	if v, ok := usage["output_tokens"].(int); ok {
		outputTokens = v
	}
	return inputTokens, outputTokens, usageEmitted, nil
}

func writeChunkLine(w io.Writer, obj map[string]interface{}) {
	line, err := json.Marshal(obj)
	if err != nil {
		_, _ = io.WriteString(w, "{\"error\":\"json_marshal_failed\"}\n")
		return
	}
	_, _ = w.Write(line)
	_, _ = w.Write([]byte("\n"))
}

func newBaseChunk(text string) map[string]interface{} {
	return map[string]interface{}{
		"text":                  text,
		"unknown_blob_names":    []interface{}{},
		"checkpoint_not_found":  false,
		"workspace_file_chunks": []interface{}{},
		"nodes":                 []interface{}{},
	}
}

func processSSEEvents(r io.Reader, handle func(eventType string, data string) error) error {
	reader := bufio.NewReader(r)
	var eventType string
	var dataLines []string
	flush := func() error {
		if len(dataLines) == 0 {
			eventType = ""
			return nil
		}
		data := strings.Join(dataLines, "\n")
		dataLines = nil
		currentEventType := eventType
		eventType = ""
		return handle(currentEventType, data)
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
		} else if strings.HasPrefix(line, ":") {
		} else {
			field := ""
			value := ""
			if idx := strings.Index(line, ":"); idx >= 0 {
				field = strings.TrimSpace(line[:idx])
				value = strings.TrimLeft(line[idx+1:], " \t")
			} else {
				field = strings.TrimSpace(line)
			}
			switch field {
			case "event":
				eventType = value
			case "data":
				dataLines = append(dataLines, value)
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	return flush()
}

func emitThinkingChunk(w io.Writer, text, signature string, nextNodeID *int) {
	if strings.TrimSpace(text) == "" && strings.TrimSpace(signature) == "" {
		return
	}
	node := map[string]interface{}{
		"id":      *nextNodeID,
		"type":    augmentNodeTypeThinking,
		"content": "",
		"thinking": map[string]interface{}{
			"summary":   text,
			"signature": signature,
		},
	}
	*nextNodeID++
	chunk := newBaseChunk("")
	chunk["nodes"] = []interface{}{node}
	writeChunkLine(w, chunk)
}

func emitToolUseChunks(w io.Writer, toolUseID, toolName, inputJSON string, nextNodeID *int) bool {
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
	startNode := map[string]interface{}{"id": *nextNodeID, "type": augmentNodeTypeToolUseStart, "content": "", "tool_use": toolUse}
	*nextNodeID++
	startChunk := newBaseChunk("")
	startChunk["nodes"] = []interface{}{startNode}
	writeChunkLine(w, startChunk)
	node := map[string]interface{}{"id": *nextNodeID, "type": augmentNodeTypeToolUse, "content": "", "tool_use": toolUse}
	*nextNodeID++
	chunk := newBaseChunk("")
	chunk["nodes"] = []interface{}{node}
	writeChunkLine(w, chunk)
	return true
}

func emitTokenUsageChunk(w io.Writer, tokenUsage map[string]interface{}, nextNodeID *int) bool {
	tokenUsage = normalizeTokenUsage(tokenUsage)
	if len(tokenUsage) == 0 {
		return false
	}
	node := map[string]interface{}{"id": *nextNodeID, "type": augmentNodeTypeTokenUsage, "content": "", "token_usage": tokenUsage}
	*nextNodeID++
	chunk := newBaseChunk("")
	chunk["nodes"] = []interface{}{node}
	writeChunkLine(w, chunk)
	return true
}

func emitFinalStopChunk(w io.Writer, stopReasonSeen bool, stopReason int, sawToolUse bool, endedCleanly bool) {
	finalReason := augmentStopReasonUnspecified
	if endedCleanly {
		if stopReasonSeen {
			finalReason = stopReason
		} else if sawToolUse {
			finalReason = augmentStopReasonToolUseRequested
		} else {
			finalReason = augmentStopReasonEndTurn
		}
	}
	chunk := newBaseChunk("")
	chunk["stop_reason"] = finalReason
	writeChunkLine(w, chunk)
}

func extractUpstreamErrorMessage(obj map[string]interface{}) string {
	if errObj := firstMap(obj, "error"); len(errObj) > 0 {
		if msg := stringValue(errObj["message"]); msg != "" {
			return msg
		}
	}
	if msg := stringValue(obj["message"]); msg != "" {
		return msg
	}
	return "claude upstream error"
}

func mapClaudeStopReason(sr string) int {
	switch strings.ToLower(strings.TrimSpace(sr)) {
	case "end_turn", "stop_sequence":
		return augmentStopReasonEndTurn
	case "max_tokens":
		return augmentStopReasonMaxTokens
	case "tool_use":
		return augmentStopReasonToolUseRequested
	case "safety":
		return augmentStopReasonSafety
	case "recitation":
		return augmentStopReasonRecitation
	default:
		return augmentStopReasonEndTurn
	}
}

func mergeUsage(dst map[string]interface{}, src map[string]interface{}) {
	if len(src) == 0 {
		return
	}
	for _, key := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		if v, ok := usageInt(src, key); ok {
			dst[key] = v
		}
	}
}

func normalizeTokenUsage(tokenUsage map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for _, key := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		if v, ok := usageInt(tokenUsage, key); ok {
			out[key] = v
		}
	}
	return out
}

func usageInt(m map[string]interface{}, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case int:
		return t, true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	default:
		return 0, false
	}
}

func firstMap(m map[string]interface{}, key string) map[string]interface{} {
	if raw, ok := m[key].(map[string]interface{}); ok {
		return raw
	}
	return nil
}

func firstArray(m map[string]interface{}, key string) []interface{} {
	if raw, ok := m[key].([]interface{}); ok {
		return raw
	}
	return nil
}
