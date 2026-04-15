package service

import "github.com/gin-gonic/gin"

func shouldApplyForcedCodexInstructionsForRequest(c *gin.Context, _ string) bool {
	if c == nil || c.Request == nil {
		return false
	}
	path := c.Request.URL.Path
	return isCursorOpenAICompatPath(path) || isCursorAnthropicCompatPath(path)
}
