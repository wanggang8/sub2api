//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

)

// --- shared test helpers ---

type queuedHTTPUpstream struct {
	responses []*http.Response
	requests  []*http.Request
	tlsFlags  []bool
}

func (u *queuedHTTPUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected Do call")
}

func (u *queuedHTTPUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, opts *UpstreamTLSOptions) (*http.Response, error) {
	u.requests = append(u.requests, req)
	u.tlsFlags = append(u.tlsFlags, opts != nil && opts.FingerprintProfile != nil)
	if len(u.responses) == 0 {
		return nil, fmt.Errorf("no mocked response")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

func newJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// --- test functions ---

func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/1/test", nil)
	return c, rec
}

type openAIAccountTestRepo struct {
	mockAccountRepoForGemini
	updatedExtra  map[string]any
	rateLimitedID int64
	rateLimitedAt *time.Time
}

func (r *openAIAccountTestRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updatedExtra = updates
	return nil
}

func (r *openAIAccountTestRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedID = id
	r.rateLimitedAt = &resetAt
	return nil
}

func TestAccountTestService_OpenAISuccessPersistsSnapshotFromHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, recorder := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))
	resp.Header.Set("x-codex-primary-used-percent", "88")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "42")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          89,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4")
	require.NoError(t, err)
	require.NotEmpty(t, repo.updatedExtra)
	require.Equal(t, 42.0, repo.updatedExtra["codex_5h_used_percent"])
	require.Equal(t, 88.0, repo.updatedExtra["codex_7d_used_percent"])
	require.Contains(t, recorder.Body.String(), "test_complete")
}

func TestAccountTestService_OpenAI429PersistsSnapshotAndRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached"}}`)
	resp.Header.Set("x-codex-primary-used-percent", "100")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "100")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	repo := &openAIAccountTestRepo{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream}
	account := &Account{
		ID:          88,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4")
	require.Error(t, err)
	require.NotEmpty(t, repo.updatedExtra)
	require.Equal(t, 100.0, repo.updatedExtra["codex_5h_used_percent"])
	require.Equal(t, int64(88), repo.rateLimitedID)
	require.NotNil(t, repo.rateLimitedAt)
	require.NotNil(t, account.RateLimitResetAt)
	if account.RateLimitResetAt != nil && repo.rateLimitedAt != nil {
		require.WithinDuration(t, *repo.rateLimitedAt, *account.RateLimitResetAt, time.Second)
	}
}

func TestAccountTestService_OpenAICustomRelayResponsesOnlyUsesResponsesEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\ndata: [DONE]\n\n"))

	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          91,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        true,
			"openai_upstream_supports_chat_completions": false,
			"openai_upstream_supports_messages":         false,
		},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/responses", upstream.requests[0].URL.Path)
	require.True(t, gjson.GetBytes(mustReadBody(t, upstream.requests[0]), "instructions").Exists())
}

func TestAccountTestService_OpenAICustomRelayChatOnlyUsesChatCompletionsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))

	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          92,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": true,
			"openai_upstream_supports_messages":         false,
		},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/chat/completions", upstream.requests[0].URL.Path)
	body := mustReadBody(t, upstream.requests[0])
	require.True(t, gjson.GetBytes(body, "messages").Exists())
	require.False(t, gjson.GetBytes(body, "instructions").Exists())
}

func TestAccountTestService_OpenAICustomRelayMessagesOnlyUsesMessagesEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"message_stop\"}\n\n"))

	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          93,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": false,
			"openai_upstream_supports_messages":         true,
		},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/messages", upstream.requests[0].URL.Path)
	body := mustReadBody(t, upstream.requests[0])
	require.True(t, gjson.GetBytes(body, "messages").Exists())
	require.False(t, gjson.GetBytes(body, "instructions").Exists())
}

func TestAccountTestService_OpenAICustomRelayWithV1BaseURLBuildsCorrectEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))

	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &AccountTestService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          94,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "test-key",
			"base_url": "https://relay.example.com/v1",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": true,
			"openai_upstream_supports_messages":         false,
		},
	}

	err := svc.testOpenAIAccountConnection(ctx, account, "gpt-5.4")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "/v1/chat/completions", upstream.requests[0].URL.Path)
}

func mustReadBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	require.NotNil(t, req)
	require.NotNil(t, req.Body)
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	return body
}
