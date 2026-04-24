package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	executorcompat "github.com/Wei-Shaw/sub2api/internal/compat/executor"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func cursorCompatError(c *gin.Context, status int, message string) {
	cursorCompatTypedError(c, status, "api_error", message)
}

func cursorCompatTypedError(c *gin.Context, status int, errType, message string) {
	c.AbortWithStatusJSON(status, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

func rewriteCursorCompatRequestBody(c *gin.Context, body []byte) {
	if c == nil || c.Request == nil {
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
}

func applyCursorCompatSession(c *gin.Context, body []byte) []byte {
	service.SetForcedCodexInstructionsEnabled(c, true)
	platform := getCompatGroupPlatform(c)
	switch platform {
	case service.PlatformOpenAI:
		promptCacheKey := cursorcompat.ResolveOpenAIPromptCacheKey(c, body)
		return cursorcompat.InjectOpenAIPromptCacheKeyIfMissing(body, promptCacheKey)
	case service.PlatformAnthropic:
		patched, _ := cursorcompat.ApplyAnthropicSession(c, body)
		return patched
	default:
		return body
	}
}

func cursorCompatRequestStream(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return false
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return false
	}
	rewriteCursorCompatRequestBody(c, body)
	return cursorCompatRequestStreamFromBody(body)
}

func cursorCompatRequestStreamFromBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var payload struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.Stream
}

func cursorCompatRequestModel(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return ""
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return ""
	}
	rewriteCursorCompatRequestBody(c, body)
	return cursorCompatRequestModelFromBody(body)
}

func cursorCompatRequestModelFromBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Model)
}

func writeCursorCompatCapturedResponse(c *gin.Context, result *executorcompat.ExecuteResult, body []byte) {
	if c == nil || result == nil {
		return
	}
	status := result.StatusCode
	if status <= 0 {
		status = http.StatusOK
	}
	for key, values := range result.Header {
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
	if len(body) > 0 && strings.TrimSpace(c.Writer.Header().Get("Content-Type")) == "" {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	c.Status(status)
	if len(body) > 0 {
		_, _ = c.Writer.Write(body)
	}
}

func buildCompatCaptureTooLargeMessage(result *executorcompat.ExecuteResult) string {
	if result == nil || result.CaptureLimit <= 0 {
		return "Compat captured response exceeded buffer limit"
	}
	return "Compat captured response exceeded buffer limit"
}

func normalizeCursorCompatError(result *executorcompat.ExecuteResult) (int, string) {
	if result == nil {
		return http.StatusBadGateway, "Compat gateway execution failed"
	}
	status := result.StatusCode
	if status <= 0 {
		status = http.StatusBadGateway
	}
	message := strings.TrimSpace(extractCursorCompatErrorMessage(result.Body))
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = "Compat gateway execution failed"
	}
	return status, message
}

func writeCursorCompatAnthropicError(c *gin.Context, result *executorcompat.ExecuteResult) {
	if c == nil {
		return
	}
	status := http.StatusBadGateway
	if result != nil && result.StatusCode > 0 {
		status = result.StatusCode
	}
	errType, message, code := extractCursorCompatAnthropicError(result.Body)
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = "Compat gateway execution failed"
	}
	if errType == "" {
		errType = "invalid_request_error"
	}
	payload := gin.H{"type": "error", "error": gin.H{"type": errType, "message": message}}
	if strings.TrimSpace(code) != "" {
		payload["error"].(gin.H)["code"] = code
	}
	c.JSON(status, payload)
}

func extractCursorCompatAnthropicError(body []byte) (errType, message, code string) {
	if len(body) == 0 {
		return "", "", ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", strings.TrimSpace(string(body)), ""
	}
	errorObj, _ := payload["error"].(map[string]any)
	if v, _ := errorObj["type"].(string); strings.TrimSpace(v) != "" {
		errType = strings.TrimSpace(v)
	}
	if errType == "" {
		if v, _ := payload["type"].(string); strings.TrimSpace(v) != "" && strings.TrimSpace(v) != "error" {
			errType = strings.TrimSpace(v)
		}
	}
	if v, _ := errorObj["message"].(string); strings.TrimSpace(v) != "" {
		message = strings.TrimSpace(v)
	}
	if message == "" {
		if v, _ := payload["message"].(string); strings.TrimSpace(v) != "" {
			message = strings.TrimSpace(v)
		}
	}
	if v, _ := errorObj["code"].(string); strings.TrimSpace(v) != "" {
		code = strings.TrimSpace(v)
	}
	if code == "" {
		if v, _ := payload["code"].(string); strings.TrimSpace(v) != "" {
			code = strings.TrimSpace(v)
		}
	}
	if errType == "" && code != "" {
		errType = code
	}
	return errType, message, code
}

func extractCursorCompatErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return strings.TrimSpace(string(body))
	}
	errorObj, _ := payload["error"].(map[string]any)
	if msg, _ := errorObj["message"].(string); strings.TrimSpace(msg) != "" {
		return strings.TrimSpace(msg)
	}
	if msg, _ := payload["message"].(string); strings.TrimSpace(msg) != "" {
		return strings.TrimSpace(msg)
	}
	if msg, _ := payload["error"].(string); strings.TrimSpace(msg) != "" {
		return strings.TrimSpace(msg)
	}
	return ""
}

func rewriteCursorCompatResponsesModel(body []byte, model string) []byte {
	if len(body) == 0 || strings.TrimSpace(model) == "" {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	payload["model"] = model
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}
