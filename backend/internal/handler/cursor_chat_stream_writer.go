package handler

import (
	"bufio"
	"net"
	"net/http"

	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	"github.com/gin-gonic/gin"
)

type cursorChatStreamWriter struct {
	gin.ResponseWriter
	state       *cursorcompat.ChatStreamState
	clientModel string
}

func newCursorChatStreamWriter(w gin.ResponseWriter, clientModel string) *cursorChatStreamWriter {
	return &cursorChatStreamWriter{
		ResponseWriter: w,
		state:          cursorcompat.NewChatStreamState(),
		clientModel:    clientModel,
	}
}

func (w *cursorChatStreamWriter) Write(data []byte) (int, error) {
	patched, err := cursorcompat.PatchChatStreamChunk(data, w.clientModel, w.state)
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

func (w *cursorChatStreamWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *cursorChatStreamWriter) Flush() {
	if w == nil {
		return
	}
	if patched := cursorcompat.FinalizeChatStream(w.clientModel, w.state); len(patched) > 0 {
		_, _ = w.ResponseWriter.Write(patched)
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
