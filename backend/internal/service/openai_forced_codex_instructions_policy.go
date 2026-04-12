package service

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func isGpt5FamilyModel(model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	if strings.Contains(model, "/") {
		parts := strings.Split(model, "/")
		model = parts[len(parts)-1]
	}
	return strings.HasPrefix(strings.ToLower(model), "gpt-5")
}

func shouldApplyForcedCodexInstructionsForRequest(c *gin.Context, model string) bool {
	if c == nil || c.Request == nil {
		return false
	}
	if !isGpt5FamilyModel(model) {
		return false
	}
	path := c.Request.URL.Path
	return isCursorOpenAICompatPath(path) || isCursorAnthropicCompatPath(path)
}

