package cursor

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolveOpenAIPromptCacheKeyStableFromBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/cursor/v1/responses", nil)
	body := []byte(`{"model":"gpt-4.1","input":"hello"}`)

	first := ResolveOpenAIPromptCacheKey(c, body)
	second := ResolveOpenAIPromptCacheKey(c, body)
	require.NotEmpty(t, first)
	require.Equal(t, first, second)
}

func TestResolveOpenAIPromptCacheKeyPrefersExistingBodyValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/cursor/v1/responses", nil)
	body := []byte(`{"model":"gpt-4.1","prompt_cache_key":"keep-me","input":"hello"}`)

	resolved := ResolveOpenAIPromptCacheKey(c, body)
	require.Equal(t, "keep-me", resolved)
}

func TestResolveAnthropicSessionIDStableFromBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/cursor/v1/messages", nil)
	body := []byte(`{"model":"claude-sonnet","messages":[{"role":"user","content":"hello"}]}`)

	first := ResolveAnthropicSessionID(c, body)
	second := ResolveAnthropicSessionID(c, body)
	require.NotEmpty(t, first)
	require.Equal(t, first, second)
}

func TestResolveAnthropicSessionIDPrefersHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/cursor/v1/messages", nil)
	c.Request.Header.Set("X-Claude-Code-Session-Id", "header-session")
	body := []byte(`{"model":"claude-sonnet","prompt_cache_key":"body-session","messages":[{"role":"user","content":"hello"}]}`)

	resolved := ResolveAnthropicSessionID(c, body)
	require.Equal(t, "header-session", resolved)
}

func TestInjectAnthropicMetadataUserIDIfMissingKeepsExistingValue(t *testing.T) {
	body := []byte(`{"metadata":{"user_id":"keep-me"}}`)
	patched := InjectAnthropicMetadataUserIDIfMissing(body, "Claude-Code/2.1.78", "session-1")
	require.JSONEq(t, string(body), string(patched))
}

func TestDeriveAnthropicContentSessionSeedIncludesInstructions(t *testing.T) {
	seed1 := deriveAnthropicContentSessionSeed([]byte(`{"model":"claude-sonnet","instructions":"alpha","messages":[{"role":"user","content":"hi"}]}`))
	seed2 := deriveAnthropicContentSessionSeed([]byte(`{"model":"claude-sonnet","instructions":"beta","messages":[{"role":"user","content":"hi"}]}`))
	require.NotEmpty(t, seed1)
	require.NotEqual(t, seed1, seed2)
}

func TestDeriveAnthropicContentSessionSeedFallsBackToInput(t *testing.T) {
	seedFromString := deriveAnthropicContentSessionSeed([]byte(`{"model":"claude-sonnet","input":"hello"}`))
	seedFromArray := deriveAnthropicContentSessionSeed([]byte(`{"model":"claude-sonnet","input":[{"type":"input_text","text":"hello"}]}`))
	require.NotEmpty(t, seedFromString)
	require.NotEmpty(t, seedFromArray)
	require.NotEqual(t, seedFromString, deriveAnthropicContentSessionSeed([]byte(`{"model":"claude-sonnet","input":"world"}`)))
	require.NotEqual(t, seedFromArray, deriveAnthropicContentSessionSeed([]byte(`{"model":"claude-sonnet","input":[{"type":"input_text","text":"world"}]}`)))
}
