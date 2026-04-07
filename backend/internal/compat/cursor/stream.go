package cursor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type MessagesStreamState struct {
	reasoningBuf  string
	thinkingShown bool
	indexOffset   int
	pending       []messagesSSEItem
}

type messagesSSEItem struct {
	eventName string
	payload   map[string]any
}

func NewMessagesStreamState() *MessagesStreamState {
	return &MessagesStreamState{}
}

func PatchMessagesStreamChunk(chunk []byte, state *MessagesStreamState) ([]byte, error) {
	if len(chunk) == 0 {
		return nil, nil
	}
	if state == nil {
		state = &MessagesStreamState{}
	}

	var output bytes.Buffer
	eventName, data, ok := parseMessagesSSEChunk(chunk)
	if !ok {
		output.Write(chunk)
		return output.Bytes(), nil
	}
	if data == "[DONE]" {
		for _, item := range flushMessagesPendingItems(state) {
			writeMessagesSSEChunk(&output, item.eventName, item.payload)
		}
		writeMessagesSSEChunk(&output, "", "[DONE]")
		return output.Bytes(), nil
	}

	payload, ok := decodeMessagesJSONObject([]byte(data))
	if !ok {
		output.Write(chunk)
		return output.Bytes(), nil
	}
	for _, item := range formatMessagesStreamEvent(eventName, payload, state) {
		writeMessagesSSEChunk(&output, item.eventName, item.payload)
	}
	return output.Bytes(), nil
}

func FinalizeMessagesStream(state *MessagesStreamState) []byte {
	if state == nil {
		return nil
	}
	var output bytes.Buffer
	for _, item := range flushMessagesPendingItems(state) {
		writeMessagesSSEChunk(&output, item.eventName, item.payload)
	}
	return output.Bytes()
}

func formatMessagesStreamEvent(eventName string, payload map[string]any, state *MessagesStreamState) []messagesSSEItem {
	if state == nil {
		return []messagesSSEItem{{eventName: eventName, payload: payload}}
	}

	modified := cloneMessagesJSONObject(payload)
	if reasoning := consumeMessagesStreamReasoning(modified); reasoning != "" {
		state.reasoningBuf += reasoning
	}

	if !state.thinkingShown && messagesEventHasIndex(modified) {
		if isMessagesTextDelta(modified) {
			results := flushMessagesPendingItems(state)
			modified = shiftMessagesIndex(modified, state.indexOffset)
			return append(results, messagesSSEItem{eventName: eventName, payload: modified})
		}
		state.pending = append(state.pending, messagesSSEItem{eventName: eventName, payload: modified})
		return nil
	}

	if len(state.pending) > 0 {
		results := flushMessagesPendingItems(state)
		modified = shiftMessagesIndex(modified, state.indexOffset)
		return append(results, messagesSSEItem{eventName: eventName, payload: modified})
	}

	modified = shiftMessagesIndex(modified, state.indexOffset)
	return []messagesSSEItem{{eventName: eventName, payload: modified}}
}

func flushMessagesPendingItems(state *MessagesStreamState) []messagesSSEItem {
	if state == nil {
		return nil
	}

	results := make([]messagesSSEItem, 0, len(state.pending)+3)
	if state.reasoningBuf != "" && !state.thinkingShown {
		state.thinkingShown = true
		state.indexOffset = 1
		results = append(results, emitMessagesThinking(state.reasoningBuf)...)
		state.reasoningBuf = ""
	}
	for _, item := range state.pending {
		results = append(results, messagesSSEItem{
			eventName: item.eventName,
			payload:   shiftMessagesIndex(item.payload, state.indexOffset),
		})
	}
	state.pending = nil
	return results
}

func consumeMessagesStreamReasoning(payload map[string]any) string {
	reasoning := ""
	for _, key := range []string{"message", "delta"} {
		container, ok := payload[key].(map[string]any)
		if !ok {
			continue
		}
		if rc := messagesStringValue(container["reasoning_content"]); rc != "" {
			reasoning += rc
			delete(container, "reasoning_content")
		}
		if rc := messagesStringValue(container["reasoningContent"]); rc != "" {
			reasoning += rc
			delete(container, "reasoningContent")
		}
	}
	return reasoning
}

func messagesEventHasIndex(payload map[string]any) bool {
	if payload == nil {
		return false
	}
	_, ok := payload["index"]
	return ok
}

func shiftMessagesIndex(payload map[string]any, offset int) map[string]any {
	if offset <= 0 || payload == nil {
		return payload
	}
	index, ok := payload["index"]
	if !ok {
		return payload
	}
	modified := cloneMessagesJSONObject(payload)
	switch v := index.(type) {
	case float64:
		modified["index"] = v + float64(offset)
	case float32:
		modified["index"] = v + float32(offset)
	case int:
		modified["index"] = v + offset
	case int8:
		modified["index"] = int(v) + offset
	case int16:
		modified["index"] = int(v) + offset
	case int32:
		modified["index"] = int(v) + offset
	case int64:
		modified["index"] = v + int64(offset)
	case json.Number:
		if iv, err := v.Int64(); err == nil {
			modified["index"] = iv + int64(offset)
		}
	}
	return modified
}

func isMessagesTextDelta(payload map[string]any) bool {
	delta, ok := payload["delta"].(map[string]any)
	if !ok {
		return false
	}
	if messagesStringValue(delta["type"]) != "text_delta" {
		return false
	}
	return messagesStringValue(delta["text"]) != ""
}

func emitMessagesThinking(text string) []messagesSSEItem {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []messagesSSEItem{
		{
			eventName: "content_block_start",
			payload: map[string]any{
				"type":  "content_block_start",
				"index": 0,
				"content_block": map[string]any{
					"type":     "thinking",
					"thinking": "",
				},
			},
		},
		{
			eventName: "content_block_delta",
			payload: map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type":     "thinking_delta",
					"thinking": text,
				},
			},
		},
		{
			eventName: "content_block_stop",
			payload: map[string]any{
				"type":  "content_block_stop",
				"index": 0,
			},
		},
	}
}

func splitMessagesSSEBundle(bundle []byte) [][]byte {
	if len(bundle) == 0 {
		return nil
	}
	normalized := bytes.ReplaceAll(bundle, []byte("\r\n"), []byte("\n"))
	rawChunks := bytes.Split(normalized, []byte("\n\n"))
	chunks := make([][]byte, 0, len(rawChunks))
	for _, raw := range rawChunks {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		chunk := raw
		if !bytes.HasSuffix(chunk, []byte("\n\n")) {
			chunk = append(chunk, []byte("\n\n")...)
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func parseMessagesSSEChunk(chunk []byte) (string, string, bool) {
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

func writeMessagesSSEChunk(buffer *bytes.Buffer, eventName string, payload any) {
	if buffer == nil {
		return
	}
	if eventName != "" {
		buffer.WriteString("event: ")
		buffer.WriteString(eventName)
		buffer.WriteByte('\n')
	}
	buffer.WriteString("data: ")
	switch value := payload.(type) {
	case string:
		buffer.WriteString(value)
	case map[string]any:
		encoded, _ := json.Marshal(value)
		buffer.Write(encoded)
	default:
		encoded, _ := json.Marshal(value)
		buffer.Write(encoded)
	}
	buffer.WriteString("\n\n")
}

func decodeMessagesJSONObject(body []byte) (map[string]any, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	return payload, true
}

func cloneMessagesJSONObject(payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(payload))
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}

func messagesStringValue(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(value)
	}
}
