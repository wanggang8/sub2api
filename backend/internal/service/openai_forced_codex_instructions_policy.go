package service

import "github.com/gin-gonic/gin"

const forcedCodexInstructionsEnabledContextKey = "openai_forced_codex_instructions_enabled"

func SetForcedCodexInstructionsEnabled(c *gin.Context, enabled bool) {
	if c == nil {
		return
	}
	c.Set(forcedCodexInstructionsEnabledContextKey, enabled)
}

func shouldApplyForcedCodexInstructionsForRequest(c *gin.Context, _ string) bool {
	if c == nil {
		return false
	}
	return c.GetBool(forcedCodexInstructionsEnabledContextKey)
}
