package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/gin-gonic/gin"
)

type forcedCodexInstructionsTemplateData struct {
	ExistingInstructions string
	OriginalModel        string
	NormalizedModel      string
	BillingModel         string
	UpstreamModel        string
}

func (d forcedCodexInstructionsTemplateData) decisionModel() string {
	for _, candidate := range []string{d.UpstreamModel, d.NormalizedModel, d.BillingModel, d.OriginalModel} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

func applyForcedCodexInstructionsTemplate(
	reqBody map[string]any,
	templateText string,
	data forcedCodexInstructionsTemplateData,
) (bool, error) {
	rendered, err := renderForcedCodexInstructionsTemplate(templateText, data)
	if err != nil {
		return false, err
	}
	if rendered == "" {
		return false, nil
	}

	existing, _ := reqBody["instructions"].(string)
	if strings.TrimSpace(existing) == rendered {
		return false, nil
	}

	reqBody["instructions"] = rendered
	return true, nil
}

func applyForcedCodexInstructionsTemplateIfNeeded(
	c *gin.Context,
	reqBody map[string]any,
	templateText string,
	data forcedCodexInstructionsTemplateData,
) (bool, error) {
	if reqBody == nil || strings.TrimSpace(templateText) == "" || !shouldApplyForcedCodexInstructionsForRequest(c, data.decisionModel()) {
		return false, nil
	}
	existingInstructions, _ := reqBody["instructions"].(string)
	data.ExistingInstructions = strings.TrimSpace(existingInstructions)
	return applyForcedCodexInstructionsTemplate(reqBody, templateText, data)
}

func applyForcedCodexInstructionsTemplateToBodyIfNeeded(
	c *gin.Context,
	body []byte,
	templateText string,
	data forcedCodexInstructionsTemplateData,
) ([]byte, bool, error) {
	if len(body) == 0 || strings.TrimSpace(templateText) == "" || !shouldApplyForcedCodexInstructionsForRequest(c, data.decisionModel()) {
		return body, false, nil
	}
	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return nil, false, fmt.Errorf("unmarshal for forced codex instructions: %w", err)
	}
	changed, err := applyForcedCodexInstructionsTemplateIfNeeded(c, reqBody, templateText, data)
	if err != nil || !changed {
		return body, false, err
	}
	patchedBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, false, fmt.Errorf("remarshal after forced codex instructions: %w", err)
	}
	return patchedBody, true, nil
}

func applyForcedCodexInstructionsTemplateToChatCompletionsBodyIfNeeded(
	c *gin.Context,
	body []byte,
	templateText string,
	data forcedCodexInstructionsTemplateData,
) ([]byte, bool, error) {
	if len(body) == 0 || strings.TrimSpace(templateText) == "" || !shouldApplyForcedCodexInstructionsForRequest(c, data.decisionModel()) {
		return body, false, nil
	}
	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return nil, false, fmt.Errorf("unmarshal for forced codex instructions chat body: %w", err)
	}

	existingInstructions := strings.TrimSpace(stringValue(reqBody["instructions"]))
	messages, ok := reqBody["messages"].([]any)
	if existingInstructions == "" && ok {
		existingInstructions = strings.TrimSpace(extractSystemInstructionsFromChatMessages(messages))
	}
	data.ExistingInstructions = existingInstructions

	rendered, err := renderForcedCodexInstructionsTemplate(templateText, data)
	if err != nil {
		return nil, false, err
	}
	if rendered == "" {
		return body, false, nil
	}

	reqBody["messages"] = injectRenderedSystemMessage(messages, rendered)
	delete(reqBody, "instructions")

	patchedBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, false, fmt.Errorf("remarshal after forced codex instructions chat body: %w", err)
	}
	return patchedBody, true, nil
}

func applyForcedCodexInstructionsTemplateToAnthropicBodyIfNeeded(
	c *gin.Context,
	body []byte,
	templateText string,
	data forcedCodexInstructionsTemplateData,
) ([]byte, bool, error) {
	if len(body) == 0 || strings.TrimSpace(templateText) == "" || !shouldApplyForcedCodexInstructionsForRequest(c, data.decisionModel()) {
		return body, false, nil
	}
	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		return nil, false, fmt.Errorf("unmarshal for forced codex instructions anthropic body: %w", err)
	}

	data.ExistingInstructions = strings.TrimSpace(extractAnthropicSystemText(reqBody["system"]))
	rendered, err := renderForcedCodexInstructionsTemplate(templateText, data)
	if err != nil {
		return nil, false, err
	}
	if rendered == "" {
		return body, false, nil
	}

	reqBody["system"] = rendered
	patchedBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, false, fmt.Errorf("remarshal after forced codex instructions anthropic body: %w", err)
	}
	return patchedBody, true, nil
}

func extractSystemInstructionsFromChatMessages(messages []any) string {
	if len(messages) == 0 {
		return ""
	}
	parts := make([]string, 0)
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(stringValue(msg["role"])), "system") {
			continue
		}
		if text := strings.TrimSpace(extractChatMessageContentText(msg["content"])); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func extractChatMessageContentText(raw any) string {
	switch typed := raw.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			partType := strings.TrimSpace(stringValue(part["type"]))
			if partType == "" || strings.EqualFold(partType, "text") {
				if text := strings.TrimSpace(stringValue(part["text"])); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n"))
	default:
		return ""
	}
}

func injectRenderedSystemMessage(messages []any, rendered string) []any {
	out := make([]any, 0, len(messages)+1)
	out = append(out, map[string]any{
		"role":    "system",
		"content": rendered,
	})
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		if strings.EqualFold(strings.TrimSpace(stringValue(msg["role"])), "system") {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func extractAnthropicSystemText(raw any) string {
	switch typed := raw.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text := strings.TrimSpace(stringValue(block["text"])); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n"))
	default:
		return ""
	}
}

func stringValue(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

func renderForcedCodexInstructionsTemplate(
	templateText string,
	data forcedCodexInstructionsTemplateData,
) (string, error) {
	tmpl, err := template.New("forced_codex_instructions").Option("missingkey=zero").Parse(templateText)
	if err != nil {
		return "", fmt.Errorf("parse forced codex instructions template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("render forced codex instructions template: %w", err)
	}

	return strings.TrimSpace(buf.String()), nil
}
