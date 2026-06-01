package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsHandlerCursorDebugEndpointsListExportAndClearRecord(t *testing.T) {
	gin.SetMode(gin.TestMode)
	debugSvc := service.NewCursorDebugService(service.CursorDebugConfig{
		Enabled:        true,
		MaxRecords:     10,
		MaxBodyBytes:   4096,
		RetentionHours: 1,
	})
	restore := service.SetDefaultCursorDebugServiceForTest(debugSvc)
	defer restore()

	captureW := httptest.NewRecorder()
	captureCtx, _ := gin.CreateTestContext(captureW)
	captureCtx.Request = httptest.NewRequest(http.MethodPost, "/cursor/v1/chat/completions", strings.NewReader("{}"))
	id := debugSvc.Begin(captureCtx, []byte(`{"model":"gpt-4.1"}`), []byte(`{"model":"gpt-4.1","messages":[]}`))
	require.NotEmpty(t, id)

	router := gin.New()
	h := NewOpsHandler(nil)
	router.GET("/records", h.ListCursorDebugRecords)
	router.GET("/records/:id", h.GetCursorDebugRecord)
	router.GET("/records/:id/export", h.ExportCursorDebugRecord)
	router.DELETE("/records", h.ClearCursorDebugRecords)

	listW := httptest.NewRecorder()
	router.ServeHTTP(listW, httptest.NewRequest(http.MethodGet, "/records", nil))
	require.Equal(t, http.StatusOK, listW.Code)
	require.Contains(t, listW.Body.String(), id)

	getW := httptest.NewRecorder()
	router.ServeHTTP(getW, httptest.NewRequest(http.MethodGet, "/records/"+id, nil))
	require.Equal(t, http.StatusOK, getW.Code)
	require.Contains(t, getW.Body.String(), id)

	exportW := httptest.NewRecorder()
	router.ServeHTTP(exportW, httptest.NewRequest(http.MethodGet, "/records/"+id+"/export", nil))
	require.Equal(t, http.StatusOK, exportW.Code)
	require.Contains(t, exportW.Header().Get("Content-Disposition"), "cursor-debug-")

	var exported service.CursorDebugRecord
	require.NoError(t, json.Unmarshal(exportW.Body.Bytes(), &exported))
	require.Equal(t, id, exported.ID)
	require.Contains(t, exported.RawRequest.Body, `"model":"gpt-4.1"`)

	clearW := httptest.NewRecorder()
	router.ServeHTTP(clearW, httptest.NewRequest(http.MethodDelete, "/records", nil))
	require.Equal(t, http.StatusOK, clearW.Code)
	require.Contains(t, clearW.Body.String(), `"deleted":1`)
	require.Equal(t, 0, debugSvc.List(1, 20).Total)
}
