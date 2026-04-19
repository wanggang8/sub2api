package handler

import (
	"bufio"
	"net"
	"net/http"

	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	"github.com/gin-gonic/gin"
)

type cursorMessagesStreamWriter struct {
	gin.ResponseWriter
	state     *cursorcompat.MessagesStreamState
	finalized bool
}

func newCursorMessagesStreamWriter(w gin.ResponseWriter) *cursorMessagesStreamWriter {
	return &cursorMessagesStreamWriter{ResponseWriter: w, state: cursorcompat.NewMessagesStreamState()}
}

func (w *cursorMessagesStreamWriter) Write(data []byte) (int, error) {
	patched, err := cursorcompat.PatchMessagesStreamChunk(data, w.state)
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

func (w *cursorMessagesStreamWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *cursorMessagesStreamWriter) Flush() {
	if !w.finalized {
		if trailing := cursorcompat.FinalizeMessagesStream(w.state); len(trailing) > 0 {
			_, _ = w.ResponseWriter.Write(trailing)
		}
		w.finalized = true
	}
	w.ResponseWriter.Flush()
}

func (w *cursorMessagesStreamWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
