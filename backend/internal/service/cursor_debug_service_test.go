package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCursorDebugServiceCapturesAndTruncatesRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewCursorDebugService(CursorDebugConfig{
		Enabled:        true,
		MaxRecords:     10,
		MaxBodyBytes:   12,
		RetentionHours: 1,
	})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader("{}"))

	id := svc.Begin(c, []byte(`{"raw":"request"}`), []byte(`{"normalized":"request"}`))
	require.NotEmpty(t, id)

	stream := true
	svc.Update(c, CursorDebugRecordPatch{
		Model:               "gpt-4.1",
		Platform:            PlatformOpenAI,
		Stream:              &stream,
		StatusCode:          http.StatusOK,
		UpstreamRequestBody: []byte(`{"tools":[{"name":"ApplyPatch"}]}`),
		RawResponseBody:     []byte("data: raw-one\n\n"),
		FinalResponseBody:   []byte("data: final-one\n\n"),
	})
	svc.Update(c, CursorDebugRecordPatch{
		RawResponseBody:   []byte("data: raw-two\n\n"),
		FinalResponseBody: []byte("data: final-two\n\n"),
	})

	record, ok := svc.Get(id)
	require.True(t, ok)
	require.Equal(t, "/cursor/v1/chat/completions", record.Path)
	require.Equal(t, "gpt-4.1", record.Model)
	require.Equal(t, PlatformOpenAI, record.Platform)
	require.True(t, record.Stream)
	require.Equal(t, http.StatusOK, record.StatusCode)
	require.True(t, record.RawRequest.Truncated)
	require.True(t, record.Normalized.Truncated)
	require.True(t, record.UpstreamRequest.Truncated)
	require.True(t, record.RawResponse.Truncated)
	require.True(t, record.FinalResponse.Truncated)
	require.LessOrEqual(t, len(record.RawResponse.Body), 12)
	require.LessOrEqual(t, len(record.FinalResponse.Body), 12)
}

func TestCursorDebugServiceDisabledDoesNotCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := NewCursorDebugService(CursorDebugConfig{Enabled: false})
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", strings.NewReader("{}"))

	require.Empty(t, svc.Begin(c, []byte("{}"), []byte("{}")))
	require.Equal(t, 0, svc.List(1, 20).Total)
}
