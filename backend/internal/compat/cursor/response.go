package cursor

import (
	"encoding/json"
	"strings"
)

func PatchMessagesResponseBody(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	reasoningRaw, ok := payload["reasoning_content"]
	if !ok {
		reasoningRaw = payload["reasoningContent"]
	}
	if len(reasoningRaw) == 0 {
		return raw, nil
	}
	delete(payload, "reasoning_content")
	delete(payload, "reasoningContent")
	var reasoning string
	if err := json.Unmarshal(reasoningRaw, &reasoning); err != nil {
		return nil, err
	}
	var content []any
	if rawContent, ok := payload["content"]; ok && len(rawContent) > 0 {
		if err := json.Unmarshal(rawContent, &content); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(reasoning) != "" {
		content = append([]any{map[string]any{"type": "thinking", "thinking": reasoning}}, content...)
	}
	payload["content"] = mustMarshal(content)
	return json.Marshal(payload)
}
