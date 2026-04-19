package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	executorcompat "github.com/Wei-Shaw/sub2api/internal/compat/executor"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAugmentCompatHandlerGetModelsTransformsStandardList(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/api/get-models", nil)

	h := &AugmentCompatHandler{
		facade: executorcompat.New(64 * 1024),
		modelsAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "application/json")
			inner.JSON(http.StatusOK, gin.H{
				"object": "list",
				"data": []gin.H{
					{"id": "claude-sonnet-4-5", "display_name": "Claude Sonnet 4.5"},
					{"id": "gpt-5.4", "display_name": "GPT-5.4"},
				},
			})
		},
	}

	h.GetModels(c)

	var models map[string]map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &models))
	require.Contains(t, models, "claude-sonnet-4-5")
	require.Equal(t, "claude-sonnet-4-5", models["claude-sonnet-4-5"]["displayName"])
	require.Equal(t, "Claude Sonnet 4.5", models["claude-sonnet-4-5"]["description"])
	require.Equal(t, "claude", models["claude-sonnet-4-5"]["shortName"])
	require.EqualValues(t, 1, models["claude-sonnet-4-5"]["priority"])
	require.Contains(t, models, "gpt-5.4")
	require.Equal(t, "gpt", models["gpt-5.4"]["shortName"])
	require.EqualValues(t, 2, models["gpt-5.4"]["priority"])
}

func TestAugmentCompatHandlerGetModelsWritesCompatErrorOnUpstreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/api/get-models", nil)

	h := &AugmentCompatHandler{
		facade: executorcompat.New(64 * 1024),
		modelsAction: func(inner *gin.Context) {
			inner.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "upstream limited"}})
		},
	}

	h.GetModels(c)

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.JSONEq(t, `{"success":false,"error":"upstream limited"}`, rec.Body.String())
}

func TestAugmentCompatHandlerGetBalanceDerivesFromAPIKeyContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/api/balance", nil)
	expiresAt := time.Now().Add(48 * time.Hour)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		ID:        1,
		Status:    service.StatusAPIKeyActive,
		Quota:     100,
		QuotaUsed: 35.5,
		ExpiresAt: &expiresAt,
		User:      &service.User{ID: 9, Balance: 12.25},
	})

	h := &AugmentCompatHandler{}
	h.Balance(c)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	data := payload["data"].(map[string]any)
	require.Equal(t, true, payload["success"])
	require.Equal(t, 64.5, data["remain_quota"])
	require.Equal(t, 12.25, data["remain_amount"])
	require.Equal(t, false, data["unlimited"])
	require.Equal(t, "enabled", data["status_text"])
	require.NotEqual(t, float64(4102444800), data["expired_time"])
}

func TestAugmentCompatHandlerGetLoginTokenUsesCurrentRequestOriginAndAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "https://proxy.example.com/usage/api/getLoginToken", nil)
	c.Request.Header.Set("X-Forwarded-Proto", "https")
	c.Request.Header.Set("X-Forwarded-Host", "proxy.example.com")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Key: "sk-augment"})

	h := &AugmentCompatHandler{}
	h.GetLoginToken(c)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	data := payload["data"].(map[string]any)
	require.Equal(t, "https://proxy.example.com", data["tenantUrl"])
	require.Equal(t, "sk-augment", data["accessToken"])
	require.Equal(t, data["tenantUrl"], payload["tenantUrl"])
	require.Equal(t, data["accessToken"], payload["accessToken"])
}

func TestAugmentCompatHandlerChatStreamAnthropicNonStreamWritesNDJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet","message":"hi","stream":false}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Key: "sk-augment", Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &AugmentCompatHandler{
		facade: executorcompat.New(64 * 1024),
		messagesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "application/json")
			inner.JSON(http.StatusOK, gin.H{
				"content":     []gin.H{{"type": "text", "text": "hello"}},
				"usage":       gin.H{"input_tokens": 2, "output_tokens": 3},
				"stop_reason": "end_turn",
			})
		},
	}

	h.ChatStream(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"text":"hello"`)
	require.Contains(t, rec.Body.String(), `"token_usage":{"input_tokens":2,"output_tokens":3}`)
}

func TestAugmentCompatHandlerChatStreamOpenAINonStreamWritesNDJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"gpt-4.1","message":"hi","stream":false}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Key: "sk-augment", Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &AugmentCompatHandler{
		facade: executorcompat.New(64 * 1024),
		openaiResponsesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "application/json")
			inner.JSON(http.StatusOK, gin.H{
				"status": "completed",
				"usage":  gin.H{"input_tokens": 4, "output_tokens": 6},
				"output": []gin.H{{
					"type":    "message",
					"role":    "assistant",
					"content": []gin.H{{"type": "output_text", "text": "hello from openai"}},
				}},
			})
		},
	}

	h.ChatStream(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"text":"hello from openai"`)
	require.Contains(t, rec.Body.String(), `"token_usage":{"input_tokens":4,"output_tokens":6}`)
}

func TestAugmentCompatHandlerChatStreamWritesErrorNDJSONForMissingAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"message":"hi"}`))

	h := &AugmentCompatHandler{}
	h.ChatStream(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"type":"error"`)
	require.Contains(t, rec.Body.String(), `"message":"Invalid API key"`)
}

func TestAugmentCompatHandlerChatStreamWritesErrorNDJSONForEmptyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(""))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Key: "sk-augment", Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &AugmentCompatHandler{messagesAction: func(*gin.Context) { t.Fatal("should not call upstream") }}
	h.ChatStream(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"message":"Request body is empty"`)
}

func TestAugmentCompatHandlerChatStreamWritesErrorNDJSONForUnsupportedGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"message":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Key: "sk-augment", Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &AugmentCompatHandler{}
	h.ChatStream(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"message":"Compat gateway is unavailable"`)
}
