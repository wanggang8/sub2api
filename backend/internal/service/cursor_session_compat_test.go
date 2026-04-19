package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolveCursorCompatPromptCacheKeyStableFromBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/cursor/v1/responses", nil)
	body := []byte(`{"model":"gpt-4.1","input":"hello"}`)

	first := ResolveCursorCompatPromptCacheKey(c, body)
	second := ResolveCursorCompatPromptCacheKey(c, body)
	require.NotEmpty(t, first)
	require.Equal(t, first, second)
}

func TestResolveCursorCompatPromptCacheKeyPrefersExistingBodyValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/cursor/v1/responses", nil)
	body := []byte(`{"model":"gpt-4.1","prompt_cache_key":"keep-me","input":"hello"}`)

	resolved := ResolveCursorCompatPromptCacheKey(c, body)
	require.Equal(t, "keep-me", resolved)
}

func TestResolveCursorCompatAnthropicSessionIDStableFromBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/cursor/v1/messages", nil)
	body := []byte(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hello"}]}`)

	first := ResolveCursorCompatAnthropicSessionID(c, body)
	second := ResolveCursorCompatAnthropicSessionID(c, body)
	require.NotEmpty(t, first)
	require.Equal(t, first, second)
}

func TestResolveCursorCompatAnthropicSessionIDPrefersHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/cursor/v1/messages", nil)
	c.Request.Header.Set("X-Claude-Code-Session-Id", "header-session")
	body := []byte(`{"model":"claude-sonnet","prompt_cache_key":"body-session","messages":[{"role":"user","content":"hello"}]}`)

	resolved := ResolveCursorCompatAnthropicSessionID(c, body)
	require.Equal(t, "header-session", resolved)
}

func TestInjectAnthropicMetadataUserIDIfMissingKeepsExistingValue(t *testing.T) {
	body := []byte(`{"metadata":{"user_id":"keep-me"}}`)
	patched := InjectAnthropicMetadataUserIDIfMissing(body, "Claude-Code/2.1.78", "session-1")
	require.JSONEq(t, string(body), string(patched))
}
