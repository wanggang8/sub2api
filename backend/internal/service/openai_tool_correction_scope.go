package service

import "github.com/gin-gonic/gin"

const openAICursorCompatRequestKey = "cursor_compat_request"

func MarkCursorCompatRequest(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(openAICursorCompatRequestKey, true)
}

func IsCursorCompatRequest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(openAICursorCompatRequestKey)
	if !ok {
		return false
	}
	marked, ok := value.(bool)
	return ok && marked
}

func shouldSkipCodexToolCorrection(c *gin.Context) bool {
	return IsCursorCompatRequest(c)
}
