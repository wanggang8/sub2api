package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResolveCursorCompatAnthropicSessionID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("returns existing Claude session header on cursor path", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", nil)
		c.Request.Header.Set("X-Claude-Code-Session-Id", "sess-existing")

		got := ResolveCursorCompatAnthropicSessionID(c, []byte(`{"model":"claude-sonnet-4"}`))
		require.Equal(t, "sess-existing", got)
	})

	t.Run("returns metadata session when already present", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", nil)

		body := []byte(`{
			"model":"claude-sonnet-4",
			"metadata":{"user_id":"{\"device_id\":\"dev\",\"account_uuid\":\"\",\"session_id\":\"12345678-1234-4234-8234-1234567890ab\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]
		}`)
		got := ResolveCursorCompatAnthropicSessionID(c, body)
		require.Equal(t, "12345678-1234-4234-8234-1234567890ab", got)
	})

	t.Run("generates deterministic session id for cursor anthropic messages", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", nil)

		body := []byte(`{
			"model":"claude-sonnet-4",
			"system":"You are a coding assistant.",
			"tools":[{"name":"Read","input_schema":{"type":"object"}}],
			"messages":[{"role":"user","content":[{"type":"text","text":"help me debug this"}]}]
		}`)

		got := ResolveCursorCompatAnthropicSessionID(c, body)
		require.Equal(t, GenerateSessionUUID(deriveAnthropicCursorContentSessionSeed(body)), got)
	})

	t.Run("keeps same key when later turns only append history", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", nil)

		round1 := []byte(`{
			"model":"claude-sonnet-4",
			"system":"You are a coding assistant.",
			"tools":[{"name":"Read","input_schema":{"type":"object"}}],
			"messages":[{"role":"user","content":[{"type":"text","text":"help me debug this"}]}]
		}`)
		round2 := []byte(`{
			"model":"claude-sonnet-4",
			"system":"You are a coding assistant.",
			"tools":[{"name":"Read","input_schema":{"type":"object"}}],
			"messages":[
				{"role":"user","content":[{"type":"text","text":"help me debug this"}]},
				{"role":"assistant","content":[{"type":"text","text":"show me the stack trace"}]},
				{"role":"user","content":[{"type":"text","text":"here it is"}]}
			]
		}`)

		key1 := ResolveCursorCompatAnthropicSessionID(c, round1)
		key2 := ResolveCursorCompatAnthropicSessionID(c, round2)
		require.NotEmpty(t, key1)
		require.Equal(t, key1, key2)
	})

	t.Run("recognizes prompt cache key on cursor chat/responses path", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", nil)

		got := ResolveCursorCompatAnthropicSessionID(c, []byte(`{"model":"CustomModel","prompt_cache_key":"compat-key-1"}`))
		require.Equal(t, "compat-key-1", got)
	})

	t.Run("skips non cursor paths", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

		got := ResolveCursorCompatAnthropicSessionID(c, []byte(`{"model":"claude-sonnet-4"}`))
		require.Empty(t, got)
	})
}

func TestApplyCursorCompatAnthropicSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "Claude-Code/2.1.90")

	body := []byte(`{
		"model":"claude-sonnet-4",
		"system":"You are a coding assistant.",
		"messages":[{"role":"user","content":[{"type":"text","text":"help me debug this"}]}]
	}`)
	parsed, err := ParseGatewayRequest(body, "")
	require.NoError(t, err)

	patchedBody, sessionID := ApplyCursorCompatAnthropicSession(c, parsed, body)
	require.NotEmpty(t, sessionID)
	require.Equal(t, sessionID, c.Request.Header.Get("X-Claude-Code-Session-Id"))
	require.Equal(t, patchedBody, parsed.Body)

	userID := gjson.GetBytes(patchedBody, "metadata.user_id").String()
	require.NotEmpty(t, userID)
	parsedUserID := ParseMetadataUserID(userID)
	require.NotNil(t, parsedUserID)
	require.Equal(t, sessionID, parsedUserID.SessionID)
	require.Equal(t, userID, parsed.MetadataUserID)
}
