package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type usageDebugServiceStub struct {
	detail     *service.UsageDebugDetail
	exportData []byte
}

func (s *usageDebugServiceStub) Get(ctx context.Context, requestID string) (*service.UsageDebugDetail, error) {
	return s.detail, nil
}

func (s *usageDebugServiceStub) ExportJSON(ctx context.Context, requestID string) ([]byte, error) {
	return s.exportData, nil
}

func TestAdminUsageHandlerGetDebugDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewUsageHandler(nil, nil, nil, nil, nil)
	h.usageDebugService = &usageDebugServiceStub{
		detail: &service.UsageDebugDetail{
			RequestID: "req-debug-1",
			CreatedAt: time.Now().UTC(),
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/admin/usage/debug/req-debug-1", nil)
	c.Params = gin.Params{{Key: "request_id", Value: "req-debug-1"}}

	h.GetDebugDetail(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"request_id":"req-debug-1"`)
}

func TestAdminUsageHandlerExportDebugDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewUsageHandler(nil, nil, nil, nil, nil)
	h.usageDebugService = &usageDebugServiceStub{
		exportData: []byte(`{"request_id":"req-debug-1"}`),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/admin/usage/debug/req-debug-1/export", nil)
	c.Params = gin.Params{{Key: "request_id", Value: "req-debug-1"}}

	h.ExportDebugDetail(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.Contains(t, rec.Header().Get("Content-Disposition"), "usage-debug-req-debug-1.json")
	require.JSONEq(t, `{"request_id":"req-debug-1"}`, rec.Body.String())
}
