package handler

import (
	"io"
	"net/http"

	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	executorcompat "github.com/Wei-Shaw/sub2api/internal/compat/executor"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func normalizeCursorRequestBody(c *gin.Context, fn func([]byte) ([]byte, error)) bool {
	_, ok := normalizeCursorRequestBodyBytes(c, fn)
	return ok
}

func normalizeCursorRequestBodyBytes(c *gin.Context, fn func([]byte) ([]byte, error)) ([]byte, bool) {
	if c == nil || c.Request == nil || fn == nil {
		return nil, true
	}
	service.MarkCursorCompatRequest(c)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		cursorCompatTypedError(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return nil, false
	}
	_ = c.Request.Body.Close()
	normalized, err := fn(raw)
	if err != nil {
		cursorCompatTypedError(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return nil, false
	}
	normalized = applyCursorCompatSession(c, normalized)
	rewriteCursorCompatRequestBody(c, normalized)
	return normalized, true
}

func (h *CursorCompatHandler) compatFacade() *executorcompat.Facade {
	if h == nil {
		return executorcompat.New(4 * 1024 * 1024)
	}
	if h.facade == nil {
		h.facade = executorcompat.New(4 * 1024 * 1024)
	}
	return h.facade
}

func patchCursorMessagesResponseBody(body []byte) []byte {
	patched, err := cursorcompat.PatchMessagesResponseBody(body)
	if err != nil {
		return body
	}
	return patched
}

func patchCursorChatResponseBody(body []byte, clientModel string) []byte {
	patched, err := cursorcompat.PatchChatResponseBody(body, clientModel)
	if err != nil {
		return body
	}
	return patched
}
