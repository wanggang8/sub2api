package handler

import (
	"bufio"
	"net"
	"net/http"

	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type cursorResponsesStreamWriter struct {
	gin.ResponseWriter
	state       *cursorcompat.ResponsesStreamState
	clientModel string
	ctx         *gin.Context
}

func newCursorResponsesStreamWriter(w gin.ResponseWriter, clientModel string, ctx *gin.Context) *cursorResponsesStreamWriter {
	return &cursorResponsesStreamWriter{ResponseWriter: w, state: cursorcompat.NewResponsesStreamState(), clientModel: clientModel, ctx: ctx}
}

func (w *cursorResponsesStreamWriter) Write(data []byte) (int, error) {
	patched, err := cursorcompat.PatchResponsesStreamChunk(data, w.clientModel, w.state)
	if err != nil {
		return 0, err
	}
	if len(patched) == 0 {
		service.CaptureCursorDebugResponse(w.ctx, data, false, nil, false, 0)
		return len(data), nil
	}
	service.CaptureCursorDebugResponse(w.ctx, data, false, patched, false, 0)
	_, err = w.ResponseWriter.Write(patched)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *cursorResponsesStreamWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *cursorResponsesStreamWriter) Finalize() {
	if w == nil {
		return
	}
	if patched := cursorcompat.FinalizeResponsesStream(w.clientModel, w.state); len(patched) > 0 {
		service.CaptureCursorDebugResponse(w.ctx, nil, false, patched, false, 0)
		_, _ = w.ResponseWriter.Write(patched)
	}
	w.Flush()
}

func (w *cursorResponsesStreamWriter) Flush() {
	if w == nil {
		return
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *cursorResponsesStreamWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
