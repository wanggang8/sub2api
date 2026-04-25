package handler

import (
	"bufio"
	"net"
	"net/http"

	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type cursorMessagesStreamWriter struct {
	gin.ResponseWriter
	state     *cursorcompat.MessagesStreamState
	finalized bool
	ctx       *gin.Context
}

func newCursorMessagesStreamWriter(w gin.ResponseWriter, ctx *gin.Context) *cursorMessagesStreamWriter {
	return &cursorMessagesStreamWriter{ResponseWriter: w, state: cursorcompat.NewMessagesStreamState(), ctx: ctx}
}

func (w *cursorMessagesStreamWriter) Write(data []byte) (int, error) {
	patched, err := cursorcompat.PatchMessagesStreamChunk(data, w.state)
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

func (w *cursorMessagesStreamWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *cursorMessagesStreamWriter) Finalize() {
	if w == nil {
		return
	}
	if !w.finalized {
		if trailing := cursorcompat.FinalizeMessagesStream(w.state); len(trailing) > 0 {
			service.CaptureCursorDebugResponse(w.ctx, nil, false, trailing, false, 0)
			_, _ = w.ResponseWriter.Write(trailing)
		}
		w.finalized = true
	}
	w.Flush()
}

func (w *cursorMessagesStreamWriter) Flush() {
	if w == nil {
		return
	}
	w.ResponseWriter.Flush()
}

func (w *cursorMessagesStreamWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
