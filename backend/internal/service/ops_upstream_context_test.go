package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSafeUpstreamURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"strips query", "https://api.anthropic.com/v1/messages?beta=true", "https://api.anthropic.com/v1/messages"},
		{"strips fragment", "https://api.openai.com/v1/responses#frag", "https://api.openai.com/v1/responses"},
		{"strips both", "https://host/path?token=secret#x", "https://host/path"},
		{"no query or fragment", "https://host/path", "https://host/path"},
		{"empty string", "", ""},
		{"whitespace only", "  ", ""},
		{"query before fragment", "https://h/p?a=1#f", "https://h/p"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, safeUpstreamURL(tt.input))
		})
	}
}

func TestSetOpsUpstreamRequestBodyRequiresCursorDebugRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)

	setOpsUpstreamRequestBody(c, []byte(`{"model":"gpt"}`))
	_, ok := c.Get(OpsUpstreamRequestBodyKey)
	require.False(t, ok)

	c.Set(cursorDebugRecordIDKey, "debug-id")
	setOpsUpstreamRequestBody(c, []byte(`{"model":"gpt"}`))
	raw, ok := c.Get(OpsUpstreamRequestBodyKey)
	require.True(t, ok)
	require.Equal(t, `{"model":"gpt"}`, raw)
}
