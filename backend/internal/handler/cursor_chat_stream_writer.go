package handler

import (
	"bufio"
	"net"
	"net/http"

	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type cursorChatStreamWriter struct {
	gin.ResponseWriter
	state       *cursorcompat.ChatStreamState
	clientModel string
	ctx         *gin.Context
}

func newCursorChatStreamWriter(w gin.ResponseWriter, clientModel string, ctx *gin.Context) *cursorChatStreamWriter {
	return &cursorChatStreamWriter{
		ResponseWriter: w,
		state:          cursorcompat.NewChatStreamState(),
		clientModel:    clientModel,
		ctx:            ctx,
	}
}

func (w *cursorChatStreamWriter) Write(data []byte) (int, error) {
	patched, err := cursorcompat.PatchChatStreamChunk(data, w.clientModel, w.state)
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

func (w *cursorChatStreamWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *cursorChatStreamWriter) Finalize() {
	if w == nil {
		return
	}
	if patched := cursorcompat.FinalizeChatStream(w.clientModel, w.state); len(patched) > 0 {
		service.CaptureCursorDebugResponse(w.ctx, nil, false, patched, false, 0)
		_, _ = w.ResponseWriter.Write(patched)
	}
	w.Flush()
}

func (w *cursorChatStreamWriter) Flush() {
	if w == nil {
		return
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *cursorChatStreamWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
