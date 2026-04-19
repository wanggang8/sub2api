package handler

import (
	"bufio"
	"net"
	"net/http"

	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	"github.com/gin-gonic/gin"
)

type cursorResponsesStreamWriter struct {
	gin.ResponseWriter
	state       *cursorcompat.ResponsesStreamState
	clientModel string
}

func newCursorResponsesStreamWriter(w gin.ResponseWriter, clientModel string) *cursorResponsesStreamWriter {
	return &cursorResponsesStreamWriter{ResponseWriter: w, state: cursorcompat.NewResponsesStreamState(), clientModel: clientModel}
}

func (w *cursorResponsesStreamWriter) Write(data []byte) (int, error) {
	patched, err := cursorcompat.PatchResponsesStreamChunk(data, w.clientModel, w.state)
	if err != nil {
		return 0, err
	}
	if len(patched) == 0 {
		return len(data), nil
	}
	_, err = w.ResponseWriter.Write(patched)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *cursorResponsesStreamWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *cursorResponsesStreamWriter) Flush() {
	if w == nil {
		return
	}
	if patched := cursorcompat.FinalizeResponsesStream(w.clientModel, w.state); len(patched) > 0 {
		_, _ = w.ResponseWriter.Write(patched)
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
