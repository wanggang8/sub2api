package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	executorcompat "github.com/Wei-Shaw/sub2api/internal/compat/executor"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type cursorCompatOpenAIHTTPUpstreamRecorder struct {
	lastReq  *http.Request
	lastBody []byte
	resp     *http.Response
	err      error
}

func (u *cursorCompatOpenAIHTTPUpstreamRecorder) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.lastReq = req
	if req != nil && req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		u.lastBody = body
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	if u.err != nil {
		return nil, u.err
	}
	return u.resp, nil
}

func (u *cursorCompatOpenAIHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

type cursorCompatOpenAIAccountRepoStub struct {
	service.AccountRepository
	accounts []service.Account
}

func (r cursorCompatOpenAIAccountRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]service.Account, error) {
	result := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Platform == platform && account.IsSchedulable() {
			result = append(result, account)
		}
	}
	return result, nil
}

func (r cursorCompatOpenAIAccountRepoStub) ListSchedulableByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	return r.ListSchedulableByGroupIDAndPlatform(ctx, 0, platform)
}

func (r cursorCompatOpenAIAccountRepoStub) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]service.Account, error) {
	return r.ListSchedulableByGroupIDAndPlatform(ctx, 0, platform)
}

type cursorCompatGatewayCacheStub struct{}

func (cursorCompatGatewayCacheStub) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	return 0, nil
}

func (cursorCompatGatewayCacheStub) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	return nil
}

func (cursorCompatGatewayCacheStub) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	return nil
}

func (cursorCompatGatewayCacheStub) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	return nil
}

func TestCursorCompatHandlerMessagesRejectsOpenAIGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := NewCursorCompatHandler(nil, nil)
	h.Messages(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"invalid_request_error","message":"Cursor messages only supports Anthropic-compatible groups"}}`, w.Body.String())
}

func TestCursorCompatHandlerCursorDebugCapturesLocalErrorFinalResponse(t *testing.T) {
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
	list := debugSvc.List(1, 10)
	require.Equal(t, 1, list.Total)
	record := list.Items[0]
	require.Equal(t, http.StatusBadRequest, record.StatusCode)
	require.Equal(t, "gpt-4.1", record.Model)
	require.Equal(t, service.PlatformOpenAI, record.Platform)
	require.False(t, record.Stream)
	require.Contains(t, record.FinalResponse.Body, "Cursor messages only supports Anthropic-compatible groups")
}

func TestCursorErrorWriterCapturesCursorDebugRecord(t *testing.T) {
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
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","stream":true}`))

	CursorErrorWriter(c, http.StatusForbidden, "group required")

	require.Equal(t, http.StatusForbidden, w.Code)
	list := debugSvc.List(1, 10)
	require.Equal(t, 1, list.Total)
	record := list.Items[0]
	require.Equal(t, "gpt-4.1", record.Model)
	require.True(t, record.Stream)
	require.Contains(t, record.FinalResponse.Body, "group required")
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
	require.Contains(t, record.FinalResponse.Body, "Invalid API key")
}

func TestCursorCompatHandlerCountTokensRejectsOpenAIGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages/count_tokens", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := NewCursorCompatHandler(nil, nil)
	h.CountTokens(c)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"not_found_error","message":"Token counting is not supported for this platform"}}`, w.Body.String())
}

func TestCursorCompatHandlerResponsesUsesOpenAIGatewayForOpenAIGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	called := false
	h := &CursorCompatHandler{openaiResponsesAction: func(c *gin.Context) { called = true; c.JSON(http.StatusOK, gin.H{"ok": true}) }}
	h.Responses(c)

	require.True(t, called)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestCursorCompatHandlerChatCompletionsUsesOpenAIGatewayForOpenAIGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	openAICalled := false
	anthropicCalled := false
	h := &CursorCompatHandler{
		openaiChatCompletionsAction: func(c *gin.Context) { openAICalled = true; c.JSON(http.StatusOK, gin.H{"ok": true}) },
		chatCompletionsAction:       func(c *gin.Context) { anthropicCalled = true; c.JSON(http.StatusOK, gin.H{"ok": true}) },
	}
	h.ChatCompletions(c)

	require.True(t, openAICalled)
	require.False(t, anthropicCalled)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestCursorCompatHandlerChatCompletionsCapturesCursorDebugRecord(t *testing.T) {
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
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{
		openaiChatCompletionsAction: func(c *gin.Context) {
			c.Set(service.OpsUpstreamRequestBodyKey, []byte(`{"model":"upstream","tools":[{"type":"function","function":{"name":"ApplyPatch"}}]}`))
			c.JSON(http.StatusOK, gin.H{
				"id":      "chatcmpl-debug",
				"object":  "chat.completion",
				"model":   "upstream",
				"choices": []gin.H{{"index": 0, "message": gin.H{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}},
			})
		},
	}
	h.ChatCompletions(c)

	require.Equal(t, http.StatusOK, w.Code)
	list := debugSvc.List(1, 10)
	require.Equal(t, 1, list.Total)
	record := list.Items[0]
	require.Equal(t, "/cursor/v1/chat/completions", record.Path)
	require.Equal(t, "gpt-4.1", record.Model)
	require.Equal(t, service.PlatformOpenAI, record.Platform)
	require.False(t, record.Stream)
	require.Contains(t, record.RawRequest.Body, `"content":"hi"`)
	require.Contains(t, record.Normalized.Body, `"messages"`)
	require.Contains(t, record.UpstreamRequest.Body, `"ApplyPatch"`)
	require.Contains(t, record.RawResponse.Body, `"model":"upstream"`)
	require.Contains(t, record.FinalResponse.Body, `"model":"gpt-4.1"`)
}

func TestCursorCompatHandlerChatCompletionsUsesAnthropicGatewayForAnthropicGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}})

	openAICalled := false
	anthropicCalled := false
	h := &CursorCompatHandler{
		openaiChatCompletionsAction: func(c *gin.Context) { openAICalled = true; c.JSON(http.StatusOK, gin.H{"ok": true}) },
		chatCompletionsAction:       func(c *gin.Context) { anthropicCalled = true; c.JSON(http.StatusOK, gin.H{"ok": true}) },
	}
	h.ChatCompletions(c)

	require.False(t, openAICalled)
	require.True(t, anthropicCalled)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestCursorCompatHandlerChatCompletionsReturnsBadGatewayWhenOpenAIHandlerMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := NewCursorCompatHandler(nil, nil)
	h.ChatCompletions(c)

	require.Equal(t, http.StatusBadGateway, w.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"api_error","message":"Compat gateway is unavailable"}}`, w.Body.String())
}

func TestCursorCompatHandlerResponsesReturnsBadGatewayWhenOpenAIHandlerMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := NewCursorCompatHandler(nil, nil)
	h.Responses(c)

	require.Equal(t, http.StatusBadGateway, w.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"api_error","message":"Compat gateway is unavailable"}}`, w.Body.String())
}

func TestCursorCompatHandlerMessagesCapturesAndPatchesNonStreamResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", strings.NewReader(`{"model":"claude","messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &CursorCompatHandler{
		messagesAction: func(c *gin.Context) {
			c.Header("Content-Type", "application/json")
			c.JSON(http.StatusOK, gin.H{
				"type":              "message",
				"content":           []gin.H{{"type": "text", "text": "done"}},
				"reasoning_content": "think",
			})
		},
	}
	h.Messages(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"type":"message","content":[{"type":"thinking","thinking":"think"},{"type":"text","text":"done"}]}`, w.Body.String())
}

func TestCursorCompatHandlerChatCompletionsOpenAIResponsesBridgeInjectsInstructions(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(11)
	apiKey := &service.APIKey{
		ID:      101,
		GroupID: &groupID,
		Group: &service.Group{
			ID:                 groupID,
			Platform:           service.PlatformOpenAI,
			DefaultMappedModel: "gpt-4o",
		},
		User: &service.User{
			ID:      7,
			Status:  service.StatusActive,
			Balance: 100,
		},
	}

	account := service.Account{
		ID:          301,
		Name:        "openai-apikey",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "api-key",
		},
	}

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-4o","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &cursorCompatOpenAIHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_handler_cursor_chat"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	cfg := &config.Config{
		RunMode: config.RunModeSimple,
		Gateway: config.GatewayConfig{
			ForcedCodexInstructionsTemplate: "cursor-handler-template",
		},
	}
	billingCacheService := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg)
	defer billingCacheService.Stop()

	gatewayService := service.NewOpenAIGatewayService(
		cursorCompatOpenAIAccountRepoStub{accounts: []service.Account{account}},
		nil,
		nil,
		nil,
		nil,
		nil,
		cursorCompatGatewayCacheStub{},
		cfg,
		nil,
		nil,
		service.NewBillingService(cfg, nil),
		nil,
		billingCacheService,
		upstream,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	openAIHandler := &OpenAIGatewayHandler{
		gatewayService:      gatewayService,
		billingCacheService: billingCacheService,
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&concurrencyCacheMock{
			acquireUserSlotFn: func(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
				return true, nil
			},
			acquireAccountSlotFn: func(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
				return true, nil
			},
		}), SSEPingFormatNone, time.Second),
		maxAccountSwitches: 1,
		cfg:                cfg,
	}

	h := &CursorCompatHandler{
		openaiGateway:               openAIHandler,
		openaiChatCompletionsAction: openAIHandler.ChatCompletions,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"CustomModel","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: apiKey.User.ID, Concurrency: 5})

	h.ChatCompletions(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "/v1/responses", upstream.lastReq.URL.Path)
	require.Equal(t, "cursor-handler-template", gjson.GetBytes(upstream.lastBody, "instructions").String())
}

func TestCursorCompatHandlerResponsesRejectsCapturedBodyOverLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{
		facade:                executorcompat.New(8),
		openaiResponsesAction: func(c *gin.Context) { c.String(http.StatusOK, strings.Repeat("a", 32)) },
	}
	h.Responses(c)

	require.Equal(t, http.StatusBadGateway, w.Code)
	require.Contains(t, w.Body.String(), "Compat captured response exceeded buffer limit")
}

func TestNormalizeCursorRequestBodyRewritesResponsesInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","input":[{"role":"user","content":"hi"}],"instructions":"sys","max_output_tokens":64,"reasoning":{"effort":"high"},"tools":[{"type":"function","name":"lookup","description":"d","parameters":{"type":"object"}},{"type":"function","name":"ApplyPatch","parameters":{"type":"object"}}],"tool_choice":{"type":"function","name":"lookup"}}`))

	ok := normalizeCursorRequestBody(c, cursorcompat.NormalizeChatCompletionsRequestBody)
	require.True(t, ok)

	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"model":"gpt-4.1","messages":[{"role":"system","content":"sys"},{"role":"user","content":"hi"}],"max_completion_tokens":64,"reasoning_effort":"high","tools":[{"type":"function","function":{"name":"lookup","description":"d","parameters":{"type":"object"}}},{"type":"function","function":{"name":"ApplyPatch","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`, string(body))
}

func TestNormalizeCursorRequestBodyRepairsMessagesToolUseInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", strings.NewReader(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"x","name":"lookup","input":""}]}],"tools":[{"name":"lookup"}]}`))

	ok := normalizeCursorRequestBody(c, cursorcompat.NormalizeMessagesRequestBody)
	require.True(t, ok)

	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.JSONEq(t, `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"x","name":"lookup","input":{}}]}],"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}]}`, string(body))
}

func TestNormalizeCursorRequestBodyInjectsPromptCacheKeyForOpenAICompat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	ok := normalizeCursorRequestBody(c, cursorcompat.NormalizeResponsesRequestBody)
	require.True(t, ok)

	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.NotEmpty(t, body)
	require.NotEmpty(t, gjson.GetBytes(body, "prompt_cache_key").String())
}

func TestNormalizeCursorRequestBodyInjectsPromptCacheKeyForOpenAIChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	ok := normalizeCursorRequestBody(c, cursorcompat.NormalizeChatCompletionsRequestBody)
	require.True(t, ok)

	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.NotEmpty(t, gjson.GetBytes(body, "prompt_cache_key").String())
}

func TestNormalizeCursorRequestBodyInjectsAnthropicSessionAndMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", strings.NewReader(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("User-Agent", "Claude-Code/2.1.78")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}})

	ok := normalizeCursorRequestBody(c, cursorcompat.NormalizeMessagesRequestBody)
	require.True(t, ok)

	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.NotEmpty(t, c.Request.Header.Get("X-Claude-Code-Session-Id"))
	require.NotEmpty(t, gjson.GetBytes(body, "metadata.user_id").String())
}

func TestNormalizeCursorRequestBodyInjectsAnthropicSessionForChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}]}`))
	c.Request.Header.Set("User-Agent", "Claude-Code/2.1.78")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}})

	ok := normalizeCursorRequestBody(c, cursorcompat.NormalizeChatCompletionsRequestBody)
	require.True(t, ok)

	body, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	require.NotEmpty(t, c.Request.Header.Get("X-Claude-Code-Session-Id"))
	require.NotEmpty(t, gjson.GetBytes(body, "metadata.user_id").String())
}

func TestCursorCompatHandlerMessagesStreamPatchesThinkingEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", strings.NewReader(`{"model":"claude-sonnet","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &CursorCompatHandler{messagesAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\",\"reasoning_content\":\"think\"}}\n\n"))
		_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	}}
	h.Messages(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `event: content_block_start`)
	require.Contains(t, w.Body.String(), `"thinking":"think"`)
	require.Contains(t, w.Body.String(), `"index":1`)
}

func TestCursorCompatHandlerMessagesStreamFlushDoesNotFinalizeMidStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", strings.NewReader(`{"model":"claude-sonnet","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &CursorCompatHandler{messagesAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"\",\"reasoning_content\":\"a\"}}\n\n"))
		c.Writer.Flush()
		_, _ = c.Writer.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\",\"reasoning_content\":\"b\"}}\n\n"))
	}}
	h.Messages(c)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, body, `"thinking":"ab"`)
	require.Contains(t, body, `"text":"hello"`)
}

func TestCursorCompatHandlerResponsesStreamPatchesCreatedAndCompleted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1","stream":true,"input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{openaiResponsesAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("event: response.output_text.delta\ndata: {\"delta\":\"hello\",\"output_index\":0}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.output_item.added\ndata: {\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]},\"output_index\":0}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.output_item.done\ndata: {\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]},\"output_index\":0}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.completed\ndata: {\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n"))
	}}
	h.Responses(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `event: response.created`)
	require.Contains(t, w.Body.String(), `"model":"gpt-4.1"`)
	require.Contains(t, w.Body.String(), `event: response.completed`)
	require.Contains(t, w.Body.String(), `"output":[{`)
}

func TestCursorCompatHandlerResponsesStreamFinalizesReasoningMessageAndTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1","stream":true,"input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{openaiResponsesAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("event: response.reasoning_summary_text.delta\ndata: {\"delta\":\"think\",\"output_index\":0,\"summary_index\":0}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.output_text.delta\ndata: {\"delta\":\"hello\",\"output_index\":1}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.function_call_arguments.delta\ndata: {\"delta\":\"{\\\"x\\\":1}\",\"item_id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"tool_a\",\"output_index\":2}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.completed\ndata: {\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n"))
	}}
	h.Responses(c)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, body, `event: response.reasoning_summary_text.done`)
	require.Contains(t, body, `event: response.output_text.done`)
	require.Contains(t, body, `event: response.function_call_arguments.done`)
	require.Contains(t, body, `"type":"reasoning"`)
	require.Contains(t, body, `"type":"message"`)
	require.Contains(t, body, `"type":"function_call"`)
	require.Contains(t, body, `"name":"tool_a"`)
	require.Contains(t, body, `"call_id":"call_1"`)
}

func TestCursorCompatHandlerResponsesStreamSupportsContentPartAndCamelCaseToolIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1","stream":true,"input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{openaiResponsesAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("event: response.output_item.added\ndata: {\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"tool_a\"},\"output_index\":0}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.function_call_arguments.delta\ndata: {\"delta\":\"{\\\"x\\\":\",\"callId\":\"call_1\"}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.function_call_arguments.done\ndata: {\"itemId\":\"fc_1\",\"arguments\":\"{\\\"x\\\":1}\"}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.content_part.added\ndata: {\"part\":{\"type\":\"output_text\",\"text\":\"hello\"}}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.content_part.done\ndata: {\"part\":{\"type\":\"output_text\",\"text\":\"hello\"}}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.output_item.done\ndata: {\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\"}}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.completed\ndata: {\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n"))
	}}
	h.Responses(c)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, body, `event: response.content_part.added`)
	require.NotContains(t, body, `event: response.content_part.done`)
	require.Contains(t, body, `"call_id":"call_1"`)
	require.Contains(t, body, `"arguments":"{\"x\":1}"`)
	require.Contains(t, body, `"output":[{"arguments":"{\"x\":1}","call_id":"call_1","id":"fc_1","name":"tool_a","status":"completed","type":"function_call"}]`)
}

func TestCursorCompatHandlerResponsesStreamIncompleteKeepsTerminalStatusWithCurrentEmptyOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1","stream":true,"input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{openaiResponsesAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("event: response.reasoning_summary_text.delta\ndata: {\"delta\":\"think\",\"output_index\":0}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.output_text.delta\ndata: {\"delta\":\"hello\",\"output_index\":1}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.incomplete\ndata: {\"response\":{\"id\":\"resp_1\",\"status\":\"incomplete\"}}\n\n"))
	}}
	h.Responses(c)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, body, `event: response.incomplete`)
	require.Contains(t, body, `"output":[]`)
	require.Contains(t, body, `"status":"incomplete"`)
}

func TestCursorCompatHandlerResponsesStreamFlushFinalizesPendingEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1","stream":true,"input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{openaiResponsesAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("event: response.reasoning_summary_text.delta\ndata: {\"delta\":\"think\",\"output_index\":0}\n\n"))
		_, _ = c.Writer.Write([]byte("event: response.output_text.delta\ndata: {\"delta\":\"hello\",\"output_index\":1}\n\n"))
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
		}
	}}
	h.Responses(c)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, body, `event: response.reasoning_summary_text.done`)
	require.Contains(t, body, `event: response.output_text.done`)
}

func TestCursorCompatHandlerResponsesStreamFlushDoesNotFinalizeMidStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1","stream":true,"input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{openaiResponsesAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("event: response.reasoning_summary_text.delta\ndata: {\"delta\":\"a\",\"output_index\":0,\"summary_index\":0}\n\n"))
		c.Writer.Flush()
		_, _ = c.Writer.Write([]byte("event: response.reasoning_summary_text.delta\ndata: {\"delta\":\"b\",\"output_index\":0,\"summary_index\":0}\n\n"))
	}}
	h.Responses(c)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)
	require.Less(t, strings.Index(body, `"delta":"b"`), strings.Index(body, `event: response.reasoning_summary_text.done`))
	require.Contains(t, body, `"text":"ab"`)
}

func TestCursorCompatHandlerChatCompletionsUsesOpenAIAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	called := false
	h := &CursorCompatHandler{openaiChatCompletionsAction: func(c *gin.Context) { called = true; c.JSON(http.StatusOK, gin.H{"ok": true}) }}
	h.ChatCompletions(c)

	require.True(t, called)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestCursorCompatHandlerChatCompletionsMarksOpenAIActionAsCursorCompat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	marked := false
	h := &CursorCompatHandler{openaiChatCompletionsAction: func(c *gin.Context) {
		marked = service.IsCursorCompatRequest(c)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}}
	h.ChatCompletions(c)

	require.True(t, marked)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestCursorCompatHandlerChatCompletionsStreamPatchesThinkTagsAndToolBoundaries(t *testing.T) {
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
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","stream":true,"input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{openaiChatCompletionsAction: func(c *gin.Context) {
		c.Set(service.OpsUpstreamRequestBodyKey, []byte(`{"model":"upstream","stream":true}`))
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"content":"<think>reason"},"finish_reason":null}]}` + "\n\n"))
		_, _ = c.Writer.Write([]byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":null}]}` + "\n\n"))
		_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	}}

	h.ChatCompletions(c)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, body, `"reasoning_content":"reason"`)
	require.Contains(t, body, `"\n\u003c/think\u003e\n\n"`)
	require.Contains(t, body, `"tool_calls":[`)
	require.Contains(t, body, `data: [DONE]`)

	list := debugSvc.List(1, 10)
	require.Equal(t, 1, list.Total)
	record := list.Items[0]
	require.True(t, record.Stream)
	require.Contains(t, record.UpstreamRequest.Body, `"stream":true`)
	require.Contains(t, record.RawResponse.Body, `<think>reason`)
	require.Contains(t, record.FinalResponse.Body, `"reasoning_content":"reason"`)
}

func TestCursorCompatHandlerChatCompletionsStreamFlushDoesNotFinalizeMidStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","stream":true,"input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{openaiChatCompletionsAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"content":"<think>a"},"finish_reason":null}]}` + "\n\n"))
		c.Writer.Flush()
		_, _ = c.Writer.Write([]byte(`data: {"id":"cmpl_1","object":"chat.completion.chunk","model":"upstream","choices":[{"index":0,"delta":{"content":"b</think>\nhello"},"finish_reason":null}]}` + "\n\n"))
		_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
	}}
	h.ChatCompletions(c)

	body := w.Body.String()
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, body, `"reasoning_content":"a"`)
	require.Contains(t, body, `"reasoning_content":"b"`)
	require.Contains(t, body, `"content":"hello"`)
	require.NotContains(t, body, `b\u003c/think`)
}

func TestCursorCompatHandlerChatCompletionsCapturesOpenAINonStreamResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	called := false
	h := &CursorCompatHandler{openaiChatCompletionsAction: func(c *gin.Context) {
		called = true
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `"messages"`)
		c.Header("Content-Type", "application/json")
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}}
	h.ChatCompletions(c)

	require.True(t, called)
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"ok":true,"model":"gpt-4.1"}`, w.Body.String())
}

func TestCursorCompatHandlerChatCompletionsPatchesNonStreamOpenAIResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{
		facade: executorcompat.New(64 * 1024),
		openaiChatCompletionsAction: func(c *gin.Context) {
			c.Header("Content-Type", "application/json")
			c.JSON(http.StatusOK, gin.H{
				"id":    "chatcmpl_1",
				"model": "upstream-model",
				"choices": []gin.H{{
					"index": 0,
					"message": gin.H{
						"role":             "assistant",
						"content":          "<think>plan</think>Hello",
						"reasoningContent": "plan",
						"function_call": gin.H{
							"name":      "read_file",
							"arguments": `{"path":"README.md"}`,
						},
					},
					"finish_reason": "function_call",
				}},
			})
		},
	}

	h.ChatCompletions(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"model":"gpt-4.1"`)
	require.Contains(t, w.Body.String(), `"reasoning_content":"plan"`)
	require.Contains(t, w.Body.String(), `"tool_calls":[`)
	require.Contains(t, w.Body.String(), `"finish_reason":"tool_calls"`)
	require.NotContains(t, w.Body.String(), `"reasoningContent"`)
	require.NotContains(t, w.Body.String(), `"function_call"`)
}

func TestCursorCompatHandlerChatCompletionsOpenAIErrorUsesNormalizedShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{
		facade: executorcompat.New(64 * 1024),
		openaiChatCompletionsAction: func(c *gin.Context) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "rate limited"}})
		},
	}
	h.ChatCompletions(c)

	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"api_error","message":"rate limited"}}`, w.Body.String())
}

func TestCursorCompatHandlerChatCompletionsAnthropicErrorPreservesErrorType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &CursorCompatHandler{
		facade: executorcompat.New(64 * 1024),
		chatCompletionsAction: func(c *gin.Context) {
			c.JSON(http.StatusBadRequest, gin.H{"type": "error", "error": gin.H{"type": "permission_error", "message": "denied", "code": "forbidden"}})
		},
	}
	h.ChatCompletions(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"permission_error","message":"denied","code":"forbidden"}}`, w.Body.String())
}

func TestCursorCompatHandlerResponsesRewritesModelForOpenAIGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1-mini","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{
		facade: executorcompat.New(64 * 1024),
		openaiResponsesAction: func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"model": "upstream-alias", "id": "resp_1", "status": "completed"})
		},
	}
	h.Responses(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"model":"gpt-4.1-mini"`)
	require.NotContains(t, w.Body.String(), `"model":"upstream-alias"`)
}

func TestCursorCompatHandlerChatCompletionsOpenAIResponsesOnlyScenarioUsesOpenAIPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	openAICalled := false
	anthropicCalled := false
	h := &CursorCompatHandler{
		openaiChatCompletionsAction: func(c *gin.Context) {
			openAICalled = true
			c.JSON(http.StatusOK, gin.H{"ok": true, "path": "openai-chat"})
		},
		chatCompletionsAction: func(c *gin.Context) {
			anthropicCalled = true
			c.JSON(http.StatusOK, gin.H{"ok": true, "path": "anthropic-chat"})
		},
	}

	h.ChatCompletions(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, openAICalled)
	require.False(t, anthropicCalled)
	require.Contains(t, w.Body.String(), `"path":"openai-chat"`)
}

func TestCursorCompatHandlerChatCompletionsChatOnlyScenarioUsesAnthropicPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}})

	openAICalled := false
	anthropicCalled := false
	h := &CursorCompatHandler{
		openaiChatCompletionsAction: func(c *gin.Context) {
			openAICalled = true
			c.JSON(http.StatusOK, gin.H{"ok": true, "path": "openai-chat"})
		},
		chatCompletionsAction: func(c *gin.Context) {
			anthropicCalled = true
			c.JSON(http.StatusOK, gin.H{"ok": true, "path": "anthropic-chat"})
		},
	}

	h.ChatCompletions(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.False(t, openAICalled)
	require.True(t, anthropicCalled)
	require.Contains(t, w.Body.String(), `"path":"anthropic-chat"`)
}

func TestCursorCompatHandlerChatCompletionsBothSupportedScenarioStillFollowsGroupPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	openAICalled := false
	anthropicCalled := false
	h := &CursorCompatHandler{
		openaiChatCompletionsAction: func(c *gin.Context) {
			openAICalled = true
			c.JSON(http.StatusOK, gin.H{"ok": true, "path": "openai-chat"})
		},
		chatCompletionsAction: func(c *gin.Context) {
			anthropicCalled = true
			c.JSON(http.StatusOK, gin.H{"ok": true, "path": "anthropic-chat"})
		},
	}

	h.ChatCompletions(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, openAICalled)
	require.False(t, anthropicCalled)
}

func TestCursorCompatHandlerChatCompletionsNoCompatibleAccountScenarioReturnsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader(`{"model":"gpt-4.1","input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{}
	h.ChatCompletions(c)

	require.Equal(t, http.StatusBadGateway, w.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"api_error","message":"Compat gateway is unavailable"}}`, w.Body.String())
}

func TestCursorCompatHandlerResponsesStreamForwardsUpstreamErrorChunk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-4.1","stream":true,"input":"hi"}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{openaiResponsesAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("event: response.error\ndata: {\"type\":\"response.error\",\"error\":{\"message\":\"boom\"}}\n\n"))
	}}
	h.Responses(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `event: response.error`)
	require.Contains(t, w.Body.String(), `"message":"boom"`)
}

func TestCursorCompatHandlerMessagesStreamForwardsUpstreamErrorChunk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", strings.NewReader(`{"model":"claude-sonnet","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &CursorCompatHandler{messagesAction: func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.Write([]byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"boom\"}}\n\n"))
	}}
	h.Messages(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `event: error`)
	require.Contains(t, w.Body.String(), `"message":"boom"`)
}
