package handler

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	executorcompat "github.com/Wei-Shaw/sub2api/internal/compat/executor"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
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

func (u *cursorCompatOpenAIHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, opts *service.UpstreamTLSOptions) (*http.Response, error) {
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

func TestAugmentCompatHandlerChatStreamTransformsStandardJSONToNDJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet-4-5","message":"hello","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformAnthropic}})

	var capturedReqBody []byte
	h := &AugmentCompatHandler{
		facade: executorcompat.New(64 * 1024),
		messagesAction: func(inner *gin.Context) {
			body, err := io.ReadAll(inner.Request.Body)
			require.NoError(t, err)
			capturedReqBody = body
			inner.Header("Content-Type", "application/json")
			inner.JSON(http.StatusOK, gin.H{
				"id":          "msg_1",
				"type":        "message",
				"role":        "assistant",
				"model":       "claude-sonnet-4-5",
				"content":     []gin.H{{"type": "text", "text": "done"}},
				"stop_reason": "end_turn",
				"usage":       gin.H{"input_tokens": 7, "output_tokens": 3},
			})
		},
	}

	h.ChatStream(c)

	var anthropicReq map[string]any
	require.NoError(t, json.Unmarshal(capturedReqBody, &anthropicReq))
	require.Equal(t, "claude-sonnet-4-5", anthropicReq["model"])
	require.Contains(t, anthropicReq, "messages")
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"text":"done"`)
	require.Contains(t, rec.Body.String(), `"stop_reason":1`)
}

func TestCursorCompatHandlerChatCompletions_OpenAIResponsesBridgeInjectsInstructions(t *testing.T) {
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
	billingCacheService := service.NewBillingCacheService(nil, nil, nil, nil, cfg)
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
		maxAccountSwitches:  1,
		cfg:                 cfg,
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

func TestAugmentCompatHandlerStreamAugmentChatPropagatesInnerContextToOuter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	outer, _ := gin.CreateTestContext(rec)
	outer.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet-4-5","message":"hello","stream":true}`))

	h := &AugmentCompatHandler{}
	err := h.streamAugmentChat(outer, []byte(`{"model":"claude-sonnet-4-5","message":"hello","stream":true}`), func(inner *gin.Context) {
		service.SetOpsUpstreamError(inner, 529, "stream upstream error", "detail")
		inner.Set(service.OpsSkipPassthroughKey, true)
		inner.Request = inner.Request.WithContext(context.WithValue(inner.Request.Context(), ctxkey.AccountID, int64(99)))
		_ = inner.Error(errors.New("stream inner failure"))
	})
	require.NoError(t, err)

	statusCodeValue, ok := outer.Get(service.OpsUpstreamStatusCodeKey)
	require.True(t, ok)
	require.Equal(t, 529, statusCodeValue)

	skipValue, ok := outer.Get(service.OpsSkipPassthroughKey)
	require.True(t, ok)
	require.Equal(t, true, skipValue)

	require.Equal(t, int64(99), outer.Request.Context().Value(ctxkey.AccountID))
	require.Len(t, outer.Errors, 1)
	require.Contains(t, outer.Errors.String(), "stream inner failure")
}

func TestAugmentCompatHandlerChatStreamDoesNotDependOnCaptureLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet-4-5","message":"hello","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformAnthropic}})
	large := strings.Repeat("done-", 60)

	h := &AugmentCompatHandler{
		facade: executorcompat.New(96),
		messagesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "text/event-stream")
			inner.Status(http.StatusOK)
			_, _ = inner.Writer.Write([]byte(strings.Join([]string{
				"event: content_block_delta",
				fmt.Sprintf(`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":%q}}`, large),
				"",
				"event: message_delta",
				`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":7,"output_tokens":3}}`,
				"",
				"event: message_stop",
				`data: {"type":"message_stop"}`,
				"",
			}, "\n")))
		},
	}

	h.ChatStream(c)

	body := rec.Body.String()
	require.Contains(t, body, large)
	require.Contains(t, body, `"stop_reason":1`)
}

func TestAugmentCompatHandlerChatStreamRoutesOpenAIGroupToResponsesCompat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{
		"model":"gpt-5",
		"message":"hello",
		"stream":false,
		"tool_definitions":[{
			"name":"read_file",
			"description":"Read a file",
			"parameters":{"type":"object","properties":{"path":{"type":"string"}}},
			"mcp_server_name":"workspace",
			"mcp_tool_name":"read_file"
		}],
		"feature_detection_flags":{"support_tool_use_start":true}
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformOpenAI}})

	var capturedReqBody []byte
	messagesCalled := false
	h := &AugmentCompatHandler{
		facade: executorcompat.New(64 * 1024),
		messagesAction: func(inner *gin.Context) {
			messagesCalled = true
			inner.Status(http.StatusOK)
		},
		openaiResponsesAction: func(inner *gin.Context) {
			body, err := io.ReadAll(inner.Request.Body)
			require.NoError(t, err)
			capturedReqBody = body
			inner.Header("Content-Type", "application/json")
			inner.JSON(http.StatusOK, gin.H{
				"id":     "resp_1",
				"object": "response",
				"model":  "gpt-5",
				"status": "completed",
				"output": []gin.H{
					{"type": "reasoning", "summary": []gin.H{{"type": "summary_text", "text": "plan"}}},
					{"type": "function_call", "call_id": "call_1", "name": "read_file", "arguments": `{"path":"README.md"}`},
					{"type": "message", "role": "assistant", "content": []gin.H{{"type": "output_text", "text": "done"}}},
				},
				"usage": gin.H{"input_tokens": 7, "output_tokens": 3},
			})
		},
	}

	h.ChatStream(c)

	require.False(t, messagesCalled)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"text":"done"`)
	require.Contains(t, rec.Body.String(), `"summary":"plan"`)
	require.Contains(t, rec.Body.String(), `"mcp_server_name":"workspace"`)
	require.Contains(t, rec.Body.String(), `"stop_reason":3`)

	var responsesReq map[string]any
	require.NoError(t, json.Unmarshal(capturedReqBody, &responsesReq))
	require.Equal(t, "gpt-5", responsesReq["model"])
	require.Contains(t, responsesReq, "input")
	require.Contains(t, responsesReq, "tools")
}

func TestAugmentCompatHandlerChatStreamStreamsOpenAIResponsesAsAugmentNDJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{
		"model":"gpt-5",
		"message":"hello",
		"stream":true,
		"tool_definitions":[{
			"name":"read_file",
			"description":"Read a file",
			"parameters":{"type":"object","properties":{"path":{"type":"string"}}},
			"mcp_server_name":"workspace",
			"mcp_tool_name":"read_file"
		}]
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &AugmentCompatHandler{
		openaiResponsesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "text/event-stream")
			inner.Status(http.StatusOK)
			_, _ = inner.Writer.Write([]byte(strings.Join([]string{
				"event: response.output_item.added",
				`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_1","name":"read_file","arguments":""}}`,
				"",
				"event: response.function_call_arguments.done",
				`data: {"type":"response.function_call_arguments.done","output_index":1,"arguments":"{\"path\":\"README.md\"}"}`,
				"",
				"event: response.output_text.delta",
				`data: {"type":"response.output_text.delta","output_index":0,"delta":"hello"}`,
				"",
				"event: response.reasoning_summary_text.done",
				`data: {"type":"response.reasoning_summary_text.done","text":"plan"}`,
				"",
				"event: response.completed",
				`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","output_text":"hello","usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`,
				"",
			}, "\n")))
		},
	}

	h.ChatStream(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"text":"hello"`)
	require.Contains(t, rec.Body.String(), `"summary":"plan"`)
	require.Contains(t, rec.Body.String(), `"tool_use_id":"call_1"`)
	require.Contains(t, rec.Body.String(), `"mcp_tool_name":"read_file"`)
	require.Contains(t, rec.Body.String(), `"stop_reason":3`)
}

func TestAugmentCompatHandlerChatStreamSurfacesOpenAIResponsesFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{
		"model":"gpt-5",
		"message":"hello",
		"stream":true
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &AugmentCompatHandler{
		openaiResponsesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "text/event-stream")
			inner.Status(http.StatusOK)
			_, _ = inner.Writer.Write([]byte(strings.Join([]string{
				"event: response.output_text.delta",
				`data: {"type":"response.output_text.delta","output_index":0,"delta":"partial"}`,
				"",
				"event: response.failed",
				`data: {"type":"response.failed","response":{"id":"resp_1","object":"response","status":"failed","error":{"message":"upstream rejected request"}}}`,
				"",
			}, "\n")))
		},
	}

	h.ChatStream(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"text":"partial"`)
	require.Contains(t, rec.Body.String(), `"type":"error"`)
	require.Contains(t, rec.Body.String(), `upstream rejected request`)
	require.NotContains(t, rec.Body.String(), `"stop_reason"`)
}

func TestAugmentCompatHandlerChatStreamUsesDecryptedStreamFlag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	encrypted := encryptAugmentPayloadWithEmbeddedKey(t, []byte(`{"model":"claude-sonnet-4-5","message":"secret prompt","stream":false}`))
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", bytes.NewReader(encrypted))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformAnthropic}})

	var capturedReqBody []byte
	h := &AugmentCompatHandler{
		facade: executorcompat.New(64 * 1024),
		messagesAction: func(inner *gin.Context) {
			body, err := io.ReadAll(inner.Request.Body)
			require.NoError(t, err)
			capturedReqBody = body
			inner.Header("Content-Type", "application/json")
			inner.JSON(http.StatusOK, gin.H{
				"id":          "msg_1",
				"type":        "message",
				"role":        "assistant",
				"model":       "claude-sonnet-4-5",
				"content":     []gin.H{{"type": "text", "text": "done"}},
				"stop_reason": "end_turn",
				"usage":       gin.H{"input_tokens": 7, "output_tokens": 3},
			})
		},
	}

	h.ChatStream(c)

	var anthropicReq map[string]any
	require.NoError(t, json.Unmarshal(capturedReqBody, &anthropicReq))
	require.Equal(t, "claude-sonnet-4-5", anthropicReq["model"])
	messages, ok := anthropicReq["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 1)
	_, hasStream := anthropicReq["stream"]
	require.False(t, hasStream)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.NotContains(t, rec.Body.String(), "event:")
	require.Contains(t, rec.Body.String(), `"text":"done"`)
}

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

func TestNewAugmentCompatHandlerNonStreamChatDoesNotUseTinyDefaultCaptureLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet-4-5","message":"hello","stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := NewAugmentCompatHandler(nil, nil)
	h.messagesAction = func(inner *gin.Context) {
		inner.Header("Content-Type", "application/json")
		inner.JSON(http.StatusOK, gin.H{
			"id":          "msg_1",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-5",
			"content":     []gin.H{{"type": "text", "text": strings.Repeat("done-", 20000)}},
			"stop_reason": "end_turn",
			"usage":       gin.H{"input_tokens": 7, "output_tokens": 3},
		})
	}

	h.ChatStream(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.NotContains(t, rec.Body.String(), "Compat captured response exceeded buffer limit")
	require.Contains(t, rec.Body.String(), `"stop_reason":1`)
	require.Contains(t, rec.Body.String(), `"text":"done-done-`)
}

func TestNewAugmentCompatHandlerGetModelsDoesNotUseTinyDefaultCaptureLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/api/get-models", nil)

	items := make([]gin.H, 0, 2500)
	for i := 0; i < 2500; i++ {
		items = append(items, gin.H{
			"id":           fmt.Sprintf("model-%04d", i),
			"display_name": strings.Repeat("Model Name ", 4) + fmt.Sprintf("%04d", i),
		})
	}

	h := NewAugmentCompatHandler(nil, nil)
	h.modelsAction = func(inner *gin.Context) {
		inner.Header("Content-Type", "application/json")
		inner.JSON(http.StatusOK, gin.H{
			"object": "list",
			"data":   items,
		})
	}

	h.GetModels(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "Compat captured response exceeded buffer limit")
	var models map[string]map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &models))
	require.Contains(t, models, "model-0000")
	require.Contains(t, models, "model-2499")
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
	h.GetBalance(c)

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

func TestCursorCompatHandlerMessagesRewritesCapturedErrorToAnthropicSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &CursorCompatHandler{
		facade: executorcompat.New(64 * 1024),
		messagesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "application/json")
			inner.JSON(http.StatusBadRequest, gin.H{"type": "error", "error": gin.H{"type": "permission_error", "message": "bad anthropic request"}})
		},
	}

	h.Messages(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"permission_error","message":"bad anthropic request"}}`, rec.Body.String())
}

func TestCursorCompatHandlerCountTokensNormalizesAndDelegatesToGateway(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages/count_tokens", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformAnthropic}})

	called := false
	h := &CursorCompatHandler{
		gateway: &GatewayHandler{},
		countTokensAction: func(inner *gin.Context) {
			called = true
			body, err := io.ReadAll(inner.Request.Body)
			require.NoError(t, err)
			require.JSONEq(t, `{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],"stream":false}`, string(body))
			inner.JSON(http.StatusOK, gin.H{"input_tokens": 12})
		},
	}

	h.CountTokens(c)

	require.True(t, called)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"input_tokens":12}`, rec.Body.String())
}

func TestCursorCompatHandlerCountTokensRejectsNonAnthropicGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages/count_tokens", strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformOpenAI}})

	called := false
	h := &CursorCompatHandler{
		countTokensAction: func(inner *gin.Context) {
			called = true
			inner.Status(http.StatusOK)
		},
	}

	h.CountTokens(c)

	require.False(t, called)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":{"message":"Token counting is not supported for this platform","type":"not_found_error"}}`, rec.Body.String())
}

func TestCursorCompatHandlerCountTokensRejectsNonAnthropicGroupBeforeBodyValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages/count_tokens", strings.NewReader(`{"model":`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformOpenAI}})

	called := false
	h := &CursorCompatHandler{
		countTokensAction: func(inner *gin.Context) {
			called = true
			inner.Status(http.StatusOK)
		},
	}

	h.CountTokens(c)

	require.False(t, called)
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.JSONEq(t, `{"error":{"message":"Token counting is not supported for this platform","type":"not_found_error"}}`, rec.Body.String())
}

func TestCursorCompatHandlerResponsesRejectsTruncatedCapturedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello","stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{
		facade: executorcompat.New(8),
		openaiResponsesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "application/json")
			_, _ = inner.Writer.Write([]byte(`{"answer":"this response is longer than eight bytes"}`))
		},
	}

	h.Responses(c)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "buffer limit (8 bytes)")
	require.NotContains(t, rec.Body.String(), `"answer"`)
}

func TestCursorCompatHandlerResponsesStreamPassesThroughOpenAIResponsesEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{
		openaiResponsesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "text/event-stream")
			inner.Status(http.StatusOK)
			_, _ = inner.Writer.Write([]byte(strings.Join([]string{
				"event: response.created",
				`data: {"type":"response.created","response":{"id":"resp_1","object":"response","status":"in_progress","model":"upstream-model","output":[]}}`,
				"",
				"event: response.output_item.added",
				`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","status":"in_progress","role":"assistant","content":[]}}`,
				"",
				"event: response.output_text.delta",
				`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hello"}`,
				"",
				"event: response.completed",
				`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed","model":"upstream-model","output":[{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}}`,
				"",
			}, "\n")))
		},
	}

	h.Responses(c)

	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	require.Contains(t, body, `event: response.output_item.added`)
	require.Contains(t, body, `"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","status":"in_progress","role":"assistant","content":[]}`)
	require.Contains(t, body, `"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"hello"`)
	require.Contains(t, body, `"model":"gpt-5"`)
	require.NotContains(t, body, `data: {"type":"message","id":"msg_1"`)
	require.NotContains(t, body, `data: {"type":"output_text","delta":"hello"}`)
}

func TestCursorCompatHandlerResponsesStreamStillPatchesAnthropicBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &CursorCompatHandler{
		responsesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "text/event-stream")
			inner.Status(http.StatusOK)
			_, _ = inner.Writer.Write([]byte(strings.Join([]string{
				`data: {"type":"response.output_text.delta","delta":"hello"}`,
				"",
				`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","status":"completed"}}`,
				"",
			}, "\n")))
		},
	}

	h.Responses(c)

	body := rec.Body.String()
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, body, `event: response.created`)
	require.Contains(t, body, `event: response.output_text.done`)
	require.Contains(t, body, `"model":"gpt-5"`)
	require.Contains(t, body, `data: {"delta":"hello","type":"output_text"}`)
}

func TestCursorCompatHandlerResponsesNonStreamRewritesOpenAIModelToClientModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader(`{"model":"gpt-5","input":"hello","stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformOpenAI}})

	h := &CursorCompatHandler{
		facade: executorcompat.New(64 * 1024),
		openaiResponsesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "application/json")
			inner.JSON(http.StatusOK, gin.H{
				"id":     "resp_1",
				"object": "response",
				"model":  "upstream-model",
				"output": []any{},
			})
		},
	}

	h.Responses(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"id":"resp_1","object":"response","model":"gpt-5","output":[]}`, rec.Body.String())
}

func TestAugmentCompatHandlerChatStreamRejectsTruncatedCapturedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet-4-5","message":"hello","stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &AugmentCompatHandler{
		facade: executorcompat.New(16),
		messagesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "application/json")
			_, _ = inner.Writer.Write([]byte(`{"type":"message","content":[{"type":"text","text":"this body is longer than sixteen bytes"}]}`))
		},
	}

	h.ChatStream(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), "buffer limit (16 bytes)")
	require.NotContains(t, rec.Body.String(), `"text":"this body`)
}

func TestAugmentCompatHandlerChatStreamPreservesThinkingSignatureInDirectStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet-4-5","message":"hello","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &AugmentCompatHandler{
		messagesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "text/event-stream")
			inner.Status(http.StatusOK)
			_, _ = inner.Writer.Write([]byte(strings.Join([]string{
				"event: content_block_delta",
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"plan"}}`,
				"",
				"event: content_block_delta",
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig_123"}}`,
				"",
				"event: message_stop",
				`data: {"type":"message_stop"}`,
				"",
			}, "\n")))
		},
	}

	h.ChatStream(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/x-ndjson", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Body.String(), `"summary":"plan"`)
	require.Contains(t, rec.Body.String(), `"signature":"sig_123"`)
}

func TestAugmentCompatHandlerChatStreamDoesNotMarkAbruptEndAsNormalStop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet-4-5","message":"hello","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformAnthropic}})

	h := &AugmentCompatHandler{
		messagesAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "text/event-stream")
			inner.Status(http.StatusOK)
			_, _ = inner.Writer.Write([]byte(strings.Join([]string{
				"event: content_block_delta",
				`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
				"",
			}, "\n")))
		},
	}

	h.ChatStream(c)

	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	require.Contains(t, lines[len(lines)-1], `"stop_reason":0`)
	require.NotContains(t, lines[len(lines)-1], `"stop_reason":1`)
}

func TestAugmentCompatHandlerGetModelsPreservesUpstreamErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/api/get-models", nil)

	h := &AugmentCompatHandler{
		facade: executorcompat.New(64 * 1024),
		modelsAction: func(inner *gin.Context) {
			inner.Header("Content-Type", "application/json")
			inner.JSON(http.StatusUnauthorized, gin.H{"error": gin.H{"message": "upstream denied"}})
		},
	}

	h.GetModels(c)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.JSONEq(t, `{"success":false,"error":"upstream denied"}`, rec.Body.String())
}

func encryptAugmentPayloadWithEmbeddedKey(t *testing.T, plaintext []byte) []byte {
	t.Helper()

	block, _ := pem.Decode([]byte(augmentcompatEmbeddedPrivateKeyPEM))
	require.NotNil(t, block)

	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	require.NoError(t, err)

	privateKey, ok := keyAny.(*rsa.PrivateKey)
	require.True(t, ok)

	aesKey := make([]byte, 32)
	aesIV := make([]byte, aes.BlockSize)
	_, err = rand.Read(aesKey)
	require.NoError(t, err)
	_, err = rand.Read(aesIV)
	require.NoError(t, err)

	blockCipher, err := aes.NewCipher(aesKey)
	require.NoError(t, err)
	padded := pkcs7PadForHandlerTest(plaintext, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(blockCipher, aesIV).CryptBlocks(ciphertext, padded)

	keyIV := append(append([]byte{}, aesKey...), aesIV...)
	encryptedKeyIV, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &privateKey.PublicKey, keyIV, nil)
	require.NoError(t, err)

	body, err := json.Marshal(map[string]any{
		"encrypted_data": base64.StdEncoding.EncodeToString(ciphertext),
		"iv":             base64.StdEncoding.EncodeToString(encryptedKeyIV),
	})
	require.NoError(t, err)
	return body
}

func pkcs7PadForHandlerTest(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padded := make([]byte, len(data)+padding)
	copy(padded, data)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	return padded
}

const augmentcompatEmbeddedPrivateKeyPEM = `-----BEGIN PRIVATE KEY-----
MIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCsvT+UKn34qpuA
GPwMps/2mjXCUbowW8FSH3tE5OZVOnoKq6uEAhX2Zzl5ZLjBo6y10qS3gBsqKLs3
ULrz7YBvJI0RkF54/kLOMvCggy9l0EQnlAzJyJ0xjE9C0rsq+6EksBF7M2bdkDyS
jaxpm/ccW9we5mZHjLoH8KZG4g4OtptdrAFHTRyK2RpTV2NgdScGPnwz5xzifsMR
JstzlQhp0eQ8yjFoukEEXraKPy0MQ2aOitmc37eOU38upxnvebhFchcIoPJwjbAn
rbbiiH4B/GiKvbCU59M4argbVjddjc/b6KY4ORXdHc7LymN2GGi9+5MPWwIcsmLc
h5C/F3xDAgMBAAECggEAAnxjTiG0IUBH3S1+3oV0nLZ62Up7O8CkAHCNQeBF/ypI
jsPlsWvJsSAYT+80kXcdQFHBJs0tclRyBCTqtP0aShF2l2XUw+9HFkW5ZezPqqqS
njvED+tW4zbMR39WAspYQDBXVKJGKrKtjHrym/XmWn5pEx562cMI+3lpr791Ep5A
nQSy0uqh3LI9+L78mggk69ix89gQAo4YTPEF8dSxHm4PwlhJCX6CnAr14O3nRR55
+GaczZzWWdekFM1mDibzGzzrAFPs/qd/KPUo8xtvvOmWb8qk7Jk9id1A/uHLVqHK
Fbo/MJWbgHhO7KK4+KG0oa+eeRLgJ1WuNjW8AiccIQKBgQDrfcX+R/gbCiunl/Lj
47qwpqGAELc65qDbaWNwckhBezmhXZPUcbULoOKAvxQ3bD+4pmYujgPyRgTFHeAj
Hi2mKxAFHfHdg/yyfon3gAkLbJwF4AfDzoj5L3aL1kVGWPsJ22AzxXbedQ8aroml
hfmoK+7mpgUSpvMSsU27YUy/OQKBgQC7yG6L53A/fnpQpqicXtLJqsm1prYqU9wy
/MnwhtPFO0ikKtBRKMbUEMaufq7SXxgq6mOfwHkiUNcpdyHa8XD0RaaPrbV4RCUC
z5ik3e8MbhXhFyVAMah9Nx1uEyQJfHkkSBnrNESO8STQtR5WgOGvmLqAo5liBm1i
z6a8MUibWwKBgD547U+Z9B3oQtCBJPSD84Dtk6aPyKwdhsruWGz6RGTqtc0zMAaJ
68eb9LrG9iwF0ZnAuxbaof1hyd4pIM7wMJgGkIdq/EERxLXtj9hS5RNcyr9cQXMW
lYvVpZNPUq1o6aAhzJGvbutxDoK7jtSUiDiu/v+9R1c9ZvqsgryVAXExAoGAbHrp
geD9s3B5cMYWed89nksPo+TfL6yqdLocXttE05ff6xbgqUIJOtFGNd/xVo6hA4nM
a6lhUTWqVsX/xN/eBP+HrVEImKWlS+5pnDSpuGCQOyyH1IHbeBqy4bglBWXnBdKx
RnM3d+xO/FLlZ8uklTCB7XaVUU+tOXwEMou2CikCgYBN205yKuscZI2Ihtg8L/QZ
hTMO2saD8L4e5u8g9JXN4t7je7fg9JaoIv+2R+qz21BIYIx0yp621gqlumb9bxXW
W1HkkwHfSDZAsjTWLVS4ZoEp+g4W9CAJ4hxWNEzMhvyed39huUgtJOg8jz3FmwWd
K9fEDfGTA4Uk0yBl+AKwlg==
-----END PRIVATE KEY-----`
