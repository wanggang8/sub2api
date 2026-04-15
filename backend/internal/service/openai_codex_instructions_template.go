package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/gin-gonic/gin"
)

const forcedCodexInstructionsStateKey = "_forced_codex_instructions_state"

type forcedCodexInstructionsTemplateData struct {
	ExistingInstructions string
	OriginalModel        string
	NormalizedModel      string
	BillingModel         string
	UpstreamModel        string
}

type forcedCodexInstructionsState struct {
	TemplateLoaded bool
	Eligible       bool
	Applied        bool
	DecisionModel  string
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

func getForcedCodexInstructionsState(c *gin.Context) (forcedCodexInstructionsState, bool) {
	if c == nil {
		return forcedCodexInstructionsState{}, false
	}
	value, ok := c.Get(forcedCodexInstructionsStateKey)
	if !ok {
		return forcedCodexInstructionsState{}, false
	}
	state, ok := value.(forcedCodexInstructionsState)
	return state, ok
}

func recordForcedCodexInstructionsState(c *gin.Context, templateText string, decisionModel string, eligible, applied bool) {
	if c == nil {
		return
	}

	state, _ := getForcedCodexInstructionsState(c)
	state.TemplateLoaded = state.TemplateLoaded || strings.TrimSpace(templateText) != ""
	state.Eligible = state.Eligible || eligible
	state.Applied = state.Applied || applied
	if strings.TrimSpace(decisionModel) != "" {
		state.DecisionModel = strings.TrimSpace(decisionModel)
	}
	c.Set(forcedCodexInstructionsStateKey, state)
}

func applyForcedCodexInstructionsTemplateIfNeeded(
	c *gin.Context,
	reqBody map[string]any,
	templateText string,
	data forcedCodexInstructionsTemplateData,
) (bool, error) {
	decisionModel := data.decisionModel()
	eligible := reqBody != nil && strings.TrimSpace(templateText) != "" && shouldApplyForcedCodexInstructionsForRequest(c, decisionModel)
	if !eligible {
		recordForcedCodexInstructionsState(c, templateText, decisionModel, false, false)
		return false, nil
	}
	existingInstructions, _ := reqBody["instructions"].(string)
	data.ExistingInstructions = strings.TrimSpace(existingInstructions)
	changed, err := applyForcedCodexInstructionsTemplate(reqBody, templateText, data)
	recordForcedCodexInstructionsState(c, templateText, decisionModel, true, err == nil && changed)
	return changed, err
}

func applyForcedCodexInstructionsTemplateToBodyIfNeeded(
	c *gin.Context,
	body []byte,
	templateText string,
	data forcedCodexInstructionsTemplateData,
) ([]byte, bool, error) {
	decisionModel := data.decisionModel()
	eligible := len(body) > 0 && strings.TrimSpace(templateText) != "" && shouldApplyForcedCodexInstructionsForRequest(c, decisionModel)
	if !eligible {
		recordForcedCodexInstructionsState(c, templateText, decisionModel, false, false)
		return body, false, nil
	}
	var reqBody map[string]any
	if err := json.Unmarshal(body, &reqBody); err != nil {
		recordForcedCodexInstructionsState(c, templateText, decisionModel, true, false)
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
