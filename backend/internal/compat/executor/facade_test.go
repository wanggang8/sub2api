package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFacadeExecuteMessagesCapturesResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet-4-5"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	facade := New(64 * 1024)
	result, err := facade.ExecuteMessages(c, []byte(`{"model":"claude-sonnet-4-5"}`), func(inner *gin.Context) {
		inner.Header("X-Test", "ok")
		inner.JSON(http.StatusCreated, gin.H{"ok": true})
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, result.StatusCode)
	require.Equal(t, "ok", result.Header.Get("X-Test"))
	require.JSONEq(t, `{"ok":true}`, string(result.Body))
	require.False(t, result.BodyTruncated)
	require.Equal(t, 64*1024, result.CaptureLimit)
}

func TestFacadeExecuteMessagesMarksTruncatedCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet-4-5"}`))

	facade := New(8)
	result, err := facade.ExecuteMessages(c, nil, func(inner *gin.Context) {
		_, _ = inner.Writer.Write([]byte(`0123456789`))
	})
	require.NoError(t, err)
	require.True(t, result.BodyTruncated)
	require.Equal(t, 8, result.CaptureLimit)
	require.Equal(t, `01234567`, string(result.Body))
}

func TestFacadeExecuteMessagesPropagatesInnerContextToOuter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/chat-stream", strings.NewReader(`{"model":"claude-sonnet-4-5"}`))

	facade := New(64 * 1024)
	_, err := facade.ExecuteMessages(c, nil, func(inner *gin.Context) {
		service.SetOpsUpstreamError(inner, 503, "upstream boom", "detail")
		inner.Set(service.OpsSkipPassthroughKey, true)
		inner.Request = inner.Request.WithContext(context.WithValue(inner.Request.Context(), ctxkey.Model, "claude-sonnet-4-5"))
		_ = inner.Error(errors.New("inner failure"))
		inner.Status(http.StatusNoContent)
	})
	require.NoError(t, err)

	statusCodeValue, ok := c.Get(service.OpsUpstreamStatusCodeKey)
	require.True(t, ok)
	require.Equal(t, 503, statusCodeValue)

	messageValue, ok := c.Get(service.OpsUpstreamErrorMessageKey)
	require.True(t, ok)
	require.Equal(t, "upstream boom", messageValue)

	skipValue, ok := c.Get(service.OpsSkipPassthroughKey)
	require.True(t, ok)
	require.Equal(t, true, skipValue)

	require.Equal(t, "claude-sonnet-4-5", c.Request.Context().Value(ctxkey.Model))
	require.Len(t, c.Errors, 1)
	require.Contains(t, c.Errors.String(), "inner failure")
}
