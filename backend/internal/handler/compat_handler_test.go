package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCursorCompatHandlerMessagesRejectsOpenAIGroupAndCapturesDebug(t *testing.T) {
	gin.SetMode(gin.TestMode)
	debugSvc := service.NewCursorDebugService(service.CursorDebugConfig{
		Enabled:        true,
		MaxRecords:     10,
		MaxBodyBytes:   4096,
		RetentionHours: 1,
	})
	restore := service.SetDefaultCursorDebugServiceForTest(debugSvc)
	defer restore()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", strings.NewReader(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	NewCursorCompatHandler(nil, nil).Messages(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"invalid_request_error","message":"Cursor messages only supports Anthropic-compatible groups"}}`, w.Body.String())

	list := debugSvc.List(1, 10)
	require.Equal(t, 1, list.Total)
	record := list.Items[0]
	require.Equal(t, http.StatusBadRequest, record.StatusCode)
	require.Equal(t, "gpt-4.1", record.Model)
	require.Equal(t, service.PlatformOpenAI, record.Platform)
	require.False(t, record.Stream)
	require.Contains(t, record.FinalResponse.Body, "Cursor messages only supports Anthropic-compatible groups")
}

func TestCursorCompatHandlerChatCompletionsOpenAIInjectsPromptCacheKeyAndCapturesDebug(t *testing.T) {
	gin.SetMode(gin.TestMode)
	debugSvc := service.NewCursorDebugService(service.CursorDebugConfig{
		Enabled:        true,
		MaxRecords:     10,
		MaxBodyBytes:   4096,
		RetentionHours: 1,
	})
	restore := service.SetDefaultCursorDebugServiceForTest(debugSvc)
	defer restore()

	var upstreamBody []byte
	h := &CursorCompatHandler{
		openaiChatCompletionsAction: func(c *gin.Context) {
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			upstreamBody = body
			c.Set(service.OpsUpstreamRequestBodyKey, body)
			c.JSON(http.StatusOK, gin.H{
				"id":      "chatcmpl-debug",
				"object":  "chat.completion",
				"model":   "upstream-model",
				"choices": []gin.H{{"index": 0, "message": gin.H{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			})
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"CustomModel","messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h.ChatCompletions(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, gjson.GetBytes(upstreamBody, "prompt_cache_key").String())
	require.JSONEq(t, `{"id":"chatcmpl-debug","object":"chat.completion","model":"CustomModel","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, w.Body.String())

	list := debugSvc.List(1, 10)
	require.Equal(t, 1, list.Total)
	record := list.Items[0]
	require.Equal(t, "/cursor/v1/chat/completions", record.Path)
	require.Equal(t, "CustomModel", record.Model)
	require.Equal(t, service.PlatformOpenAI, record.Platform)
	require.Contains(t, record.RawRequest.Body, `"content":"hi"`)
	require.NotEmpty(t, gjson.Get(record.Normalized.Body, "prompt_cache_key").String())
	require.Contains(t, record.UpstreamRequest.Body, `"prompt_cache_key"`)
	require.Contains(t, record.FinalResponse.Body, `"model":"CustomModel"`)
}

func TestCursorAuthErrorWriterCapturesCursorDebugRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)
	debugSvc := service.NewCursorDebugService(service.CursorDebugConfig{
		Enabled:        true,
		MaxRecords:     10,
		MaxBodyBytes:   4096,
		RetentionHours: 1,
	})
	restore := service.SetDefaultCursorDebugServiceForTest(debugSvc)
	defer restore()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1-mini","stream":false}`))

	CursorAuthErrorWriter(c, http.StatusUnauthorized, "INVALID_API_KEY", "Invalid API key")

	require.Equal(t, http.StatusUnauthorized, w.Code)
	list := debugSvc.List(1, 10)
	require.Equal(t, 1, list.Total)
	record := list.Items[0]
	require.Equal(t, "gpt-4.1-mini", record.Model)
	require.False(t, record.Stream)
	require.WithinDuration(t, time.Now().UTC(), record.UpdatedAt, time.Minute)
	require.Contains(t, record.FinalResponse.Body, "Invalid API key")
}
