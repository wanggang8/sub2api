package admin

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetCursorDebugConfig returns the in-memory Cursor debug capture settings.
// GET /api/v1/admin/ops/cursor-debug/config
func (h *OpsHandler) GetCursorDebugConfig(c *gin.Context) {
	response.Success(c, service.DefaultCursorDebugService().GetConfig())
}

// UpdateCursorDebugConfig updates the in-memory Cursor debug capture settings.
// PUT /api/v1/admin/ops/cursor-debug/config
func (h *OpsHandler) UpdateCursorDebugConfig(c *gin.Context) {
	var req service.CursorDebugConfigUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid cursor debug config")
		return
	}
	response.Success(c, service.DefaultCursorDebugService().UpdateConfig(req))
}

// ListCursorDebugRecords lists recent Cursor debug captures.
// GET /api/v1/admin/ops/cursor-debug/records
func (h *OpsHandler) ListCursorDebugRecords(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	result := service.DefaultCursorDebugService().List(page, pageSize)
	response.Paginated(c, result.Items, int64(result.Total), result.Page, result.PageSize)
}

// GetCursorDebugRecord returns one Cursor debug capture.
// GET /api/v1/admin/ops/cursor-debug/records/:id
func (h *OpsHandler) GetCursorDebugRecord(c *gin.Context) {
	record, ok := service.DefaultCursorDebugService().Get(strings.TrimSpace(c.Param("id")))
	if !ok {
		response.Error(c, http.StatusNotFound, "Cursor debug record not found")
		return
	}
	response.Success(c, record)
}

// ExportCursorDebugRecord returns the same record payload with download headers.
// GET /api/v1/admin/ops/cursor-debug/records/:id/export
func (h *OpsHandler) ExportCursorDebugRecord(c *gin.Context) {
	record, ok := service.DefaultCursorDebugService().Get(strings.TrimSpace(c.Param("id")))
	if !ok {
		response.Error(c, http.StatusNotFound, "Cursor debug record not found")
		return
	}
	filenameID := strings.NewReplacer("/", "-", "\\", "-", `"`, "", "'", "").Replace(record.ID)
	filename := "cursor-debug-" + filenameID + ".json"
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.JSON(http.StatusOK, record)
}

// ClearCursorDebugRecords clears all in-memory Cursor debug captures.
// DELETE /api/v1/admin/ops/cursor-debug/records
func (h *OpsHandler) ClearCursorDebugRecords(c *gin.Context) {
	deleted := service.DefaultCursorDebugService().Clear()
	response.Success(c, gin.H{"deleted": deleted})
}
