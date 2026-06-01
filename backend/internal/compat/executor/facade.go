package executor

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
)

type ExecuteResult struct {
	StatusCode           int
	Header               http.Header
	Body                 []byte
	BodyTruncated        bool
	CaptureLimit         int
	RequestContentLength int64
}

type Facade struct {
	captureLimit int
}

func New(captureLimit int) *Facade {
	if captureLimit <= 0 {
		captureLimit = 64 * 1024
	}
	return &Facade{captureLimit: captureLimit}
}

func (f *Facade) ExecuteMessages(c *gin.Context, body []byte, execute func(*gin.Context)) (*ExecuteResult, error) {
	return f.execute(c, body, execute)
}

func (f *Facade) execute(c *gin.Context, body []byte, execute func(*gin.Context)) (*ExecuteResult, error) {
	if execute == nil {
		return nil, nil
	}
	writer := newCaptureWriter(f.captureLimit)
	inner := cloneContext(c, writer, body)
	execute(inner)
	syncCompatContextState(c, inner)
	return &ExecuteResult{
		StatusCode:           writer.Status(),
		Header:               writer.Header().Clone(),
		Body:                 append([]byte(nil), writer.bodyBytes()...),
		BodyTruncated:        writer.Truncated(),
		CaptureLimit:         writer.limit,
		RequestContentLength: inner.Request.ContentLength,
	}, nil
}

func cloneContext(c *gin.Context, writer gin.ResponseWriter, body []byte) *gin.Context {
	var inner *gin.Context
	if c != nil {
		inner = c.Copy()
	} else {
		rec := newCaptureWriter(64 * 1024)
		inner, _ = gin.CreateTestContext(rec)
		inner.Request, _ = http.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(nil))
	}
	if inner.Request == nil {
		inner.Request, _ = http.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(nil))
	}
	if body != nil {
		inner.Request.Body = ioNopCloser{bytes.NewReader(body)}
		inner.Request.ContentLength = int64(len(body))
	}
	inner.Writer = writer
	return inner
}

func syncCompatContextState(outer, inner *gin.Context) {
	if outer == nil || inner == nil || outer == inner {
		return
	}
	if inner.Keys != nil {
		if outer.Keys == nil {
			outer.Keys = make(map[string]any, len(inner.Keys))
		}
		for key, value := range inner.Keys {
			outer.Keys[key] = value
		}
	}
	if len(inner.Errors) > 0 {
		outer.Errors = append(outer.Errors, inner.Errors...)
	}
	if inner.Request != nil {
		if outer.Request != nil {
			outer.Request = outer.Request.WithContext(inner.Request.Context())
		} else {
			outer.Request = inner.Request
		}
	}
}

type captureWriter struct {
	header      http.Header
	status      int
	size        int
	limit       int
	truncated   bool
	wroteHeader bool
	buf         bytes.Buffer
}

func newCaptureWriter(limit int) *captureWriter {
	if limit <= 0 {
		limit = 1
	}
	return &captureWriter{header: make(http.Header), status: http.StatusOK, size: -1, limit: limit}
}

func (w *captureWriter) Header() http.Header { return w.header }
func (w *captureWriter) WriteHeader(code int) {
	if code > 0 && !w.Written() {
		w.status = code
	}
}
func (w *captureWriter) WriteHeaderNow() {
	if !w.Written() {
		w.size = 0
		w.wroteHeader = true
	}
}
func (w *captureWriter) Write(p []byte) (int, error) {
	w.WriteHeaderNow()
	if w.buf.Len() < w.limit {
		remaining := w.limit - w.buf.Len()
		if len(p) > remaining {
			_, _ = w.buf.Write(p[:remaining])
			w.truncated = true
		} else {
			_, _ = w.buf.Write(p)
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	w.size += len(p)
	return len(p), nil
}
func (w *captureWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *captureWriter) Status() int                       { return w.status }
func (w *captureWriter) Size() int                         { return w.size }
func (w *captureWriter) Written() bool                     { return w.size >= 0 }
func (w *captureWriter) Flush()                            { w.WriteHeaderNow() }
func (w *captureWriter) CloseNotify() <-chan bool          { ch := make(chan bool); return ch }
func (w *captureWriter) Pusher() http.Pusher               { return nil }
func (w *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, http.ErrNotSupported
}
func (w *captureWriter) bodyBytes() []byte { return w.buf.Bytes() }
func (w *captureWriter) Truncated() bool   { return w.truncated }

type ioNopCloser struct{ *bytes.Reader }

func (ioNopCloser) Close() error { return nil }
