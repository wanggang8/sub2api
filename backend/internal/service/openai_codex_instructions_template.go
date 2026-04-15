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
