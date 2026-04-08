package handler

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	augmentcompat "github.com/Wei-Shaw/sub2api/internal/compat/augment"
	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	executorcompat "github.com/Wei-Shaw/sub2api/internal/compat/executor"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type CursorCompatHandler struct {
	gateway                     *GatewayHandler
	openaiGateway               *OpenAIGatewayHandler
	facade                      *executorcompat.Facade
	responsesAction             func(*gin.Context)
	chatCompletionsAction       func(*gin.Context)
	openaiResponsesAction       func(*gin.Context)
	openaiChatCompletionsAction func(*gin.Context)
	messagesAction              func(*gin.Context)
	countTokensAction           func(*gin.Context)
	openaiMessagesAction        func(*gin.Context)
	modelsAction                func(*gin.Context)
}

func NewCursorCompatHandler(gatewayHandler *GatewayHandler, openaiGatewayHandler *OpenAIGatewayHandler) *CursorCompatHandler {
	h := &CursorCompatHandler{
		gateway:       gatewayHandler,
		openaiGateway: openaiGatewayHandler,
		facade:        executorcompat.New(4 * 1024 * 1024),
	}
	if gatewayHandler != nil {
		h.responsesAction = gatewayHandler.Responses
		h.chatCompletionsAction = gatewayHandler.ChatCompletions
		h.messagesAction = gatewayHandler.Messages
		h.countTokensAction = gatewayHandler.CountTokens
		h.modelsAction = gatewayHandler.Models
	}
	if openaiGatewayHandler != nil {
		h.openaiResponsesAction = openaiGatewayHandler.Responses
		h.openaiChatCompletionsAction = openaiGatewayHandler.ChatCompletions
		h.openaiMessagesAction = openaiGatewayHandler.Messages
	}
	return h
}

func (h *CursorCompatHandler) Responses(c *gin.Context) {
	applyCursorResponsesStreamCompat(c)
	body, ok := h.normalizeRequest(c, cursorcompat.NormalizeResponsesRequestBody)
	if !ok {
		return
	}

	stream := isCursorResponsesStream(body)
	compatLog := logger.FromContext(c.Request.Context()).With(
		zap.String("compat_endpoint", "responses"),
		zap.Bool("stream", stream),
		zap.String("group_platform", getCompatGroupPlatform(c)),
	)

	action := h.responsesAction
	if getCompatGroupPlatform(c) == service.PlatformOpenAI {
		action = h.openaiResponsesAction
	}
	if action == nil {
		compatLog.Warn("cursor compat responses: action unavailable")
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}

	if stream {
		if err := h.streamCursorResponses(c, body, action); err != nil {
			compatLog.Warn("cursor compat responses: stream execution failed", zap.Error(err))
			cursorCompatError(c, http.StatusBadGateway, "Compat gateway execution failed")
		}
		return
	}

	result, err := h.compatFacade().ExecuteMessages(c, body, action)
	if err != nil || result == nil {
		if err != nil {
			compatLog.Warn("cursor compat responses: execution failed", zap.Error(err))
		} else {
			compatLog.Warn("cursor compat responses: execution returned nil result")
		}
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway execution failed")
		return
	}
	if result.BodyTruncated {
		compatLog.Warn("cursor compat responses: captured body truncated", zap.Int("status_code", result.StatusCode))
		cursorCompatError(c, http.StatusBadGateway, buildCompatCaptureTooLargeMessage(result))
		return
	}

	responseBody := result.Body
	contentType := strings.ToLower(strings.TrimSpace(result.Header.Get("Content-Type")))
	if result.StatusCode >= http.StatusBadRequest {
		status, message := normalizeCursorResponsesError(result)
		compatLog.Warn("cursor compat responses: upstream returned error",
			zap.Int("upstream_status_code", result.StatusCode),
			zap.Int("normalized_status_code", status),
		)
		cursorCompatError(c, status, message)
		return
	}
	if !strings.Contains(contentType, "text/event-stream") {
		if getCompatGroupPlatform(c) == service.PlatformOpenAI {
			responseBody = rewriteCursorResponsesModel(result.Body, cursorRequestModel(body))
		} else {
			responseBody = result.Body
		}
	}
	writeCursorCompatCapturedResponse(c, result, responseBody)
}

func (h *CursorCompatHandler) ChatCompletions(c *gin.Context) {
	applyCursorChatCompletionsStreamCompat(c)
	body, ok := h.normalizeRequest(c, cursorcompat.NormalizeChatCompletionsRequestBody)
	if !ok {
		return
	}
	compatLog := logger.FromContext(c.Request.Context()).With(
		zap.String("compat_endpoint", "chat_completions"),
		zap.String("group_platform", getCompatGroupPlatform(c)),
		zap.Bool("stream", isCursorMessagesStream(body)),
	)
	_ = body
	if getCompatGroupPlatform(c) == service.PlatformOpenAI {
		if h.openaiChatCompletionsAction == nil {
			compatLog.Warn("cursor compat chat_completions: openai action unavailable")
			cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
			return
		}
		h.openaiChatCompletionsAction(c)
		return
	}
	if h.chatCompletionsAction == nil {
		compatLog.Warn("cursor compat chat_completions: action unavailable")
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}
	h.chatCompletionsAction(c)
}

func (h *CursorCompatHandler) Messages(c *gin.Context) {
	applyCursorMessagesStreamCompat(c)
	body, ok := h.normalizeRequest(c, cursorcompat.NormalizeMessagesRequestBody)
	if !ok {
		return
	}
	stream := isCursorMessagesStream(body)
	compatLog := logger.FromContext(c.Request.Context()).With(
		zap.String("compat_endpoint", "messages"),
		zap.Bool("stream", stream),
		zap.String("group_platform", getCompatGroupPlatform(c)),
	)
	if getCompatGroupPlatform(c) != service.PlatformAnthropic {
		compatLog.Warn("cursor compat messages: unsupported platform")
		cursorCompatError(c, http.StatusBadRequest, "Cursor messages only supports Anthropic-compatible groups")
		return
	}
	if h.messagesAction == nil {
		compatLog.Warn("cursor compat messages: action unavailable")
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}
	if stream {
		if err := h.streamCursorMessages(c, body, h.messagesAction); err != nil {
			compatLog.Warn("cursor compat messages: stream execution failed", zap.Error(err))
			cursorCompatError(c, http.StatusBadGateway, "Compat gateway execution failed")
		}
		return
	}

	result, err := h.compatFacade().ExecuteMessages(c, body, h.messagesAction)
	if err != nil || result == nil {
		if err != nil {
			compatLog.Warn("cursor compat messages: execution failed", zap.Error(err))
		} else {
			compatLog.Warn("cursor compat messages: execution returned nil result")
		}
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway execution failed")
		return
	}
	if result.BodyTruncated {
		compatLog.Warn("cursor compat messages: captured body truncated", zap.Int("status_code", result.StatusCode))
		cursorCompatError(c, http.StatusBadGateway, buildCompatCaptureTooLargeMessage(result))
		return
	}
	if result.StatusCode >= http.StatusBadRequest {
		compatLog.Warn("cursor compat messages: upstream returned error", zap.Int("upstream_status_code", result.StatusCode))
		writeCursorCompatAnthropicError(c, result)
		return
	}

	responseBody := result.Body
	contentType := strings.ToLower(strings.TrimSpace(result.Header.Get("Content-Type")))
	if !strings.Contains(contentType, "text/event-stream") {
		if patched, patchErr := cursorcompat.PatchMessagesResponseBody(result.Body); patchErr == nil {
			responseBody = patched
		}
	}
	writeCursorCompatCapturedResponse(c, result, responseBody)
}

func (h *CursorCompatHandler) Models(c *gin.Context) {
	if h == nil || h.modelsAction == nil {
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}
	h.modelsAction(c)
}

func (h *CursorCompatHandler) CountTokens(c *gin.Context) {
	if getCompatGroupPlatform(c) != service.PlatformAnthropic {
		cursorCompatTypedError(c, http.StatusNotFound, "not_found_error", "Token counting is not supported for this platform")
		return
	}
	_, ok := h.normalizeRequest(c, cursorcompat.NormalizeMessagesRequestBody)
	if !ok {
		return
	}
	if h == nil || h.countTokensAction == nil {
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}
	h.countTokensAction(c)
}

func (h *CursorCompatHandler) normalizeRequest(c *gin.Context, normalize func([]byte) ([]byte, error)) ([]byte, bool) {
	if c == nil || c.Request == nil {
		return nil, false
	}
	if normalize == nil {
		return nil, true
	}
	body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			cursorCompatError(c, http.StatusRequestEntityTooLarge, buildBodyTooLargeMessage(maxErr.Limit))
			return nil, false
		}
		cursorCompatError(c, http.StatusBadRequest, "Failed to read request body")
		return nil, false
	}
	if len(body) == 0 {
		cursorCompatError(c, http.StatusBadRequest, "Request body is empty")
		return nil, false
	}
	if _, ok := middleware2.GetAPIKeyFromContext(c); !ok {
		cursorCompatError(c, http.StatusUnauthorized, "Invalid API key")
		return nil, false
	}

	normalized, err := normalize(body)
	if err != nil {
		cursorCompatError(c, http.StatusBadRequest, fmt.Sprintf("Failed to normalize request body: %v", err))
		return nil, false
	}
	rewriteRequestBody(c, normalized)
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

func isCursorMessagesStream(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var payload struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.Stream
}

func isCursorResponsesStream(body []byte) bool {
	return isCursorMessagesStream(body)
}

func cursorRequestModel(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Model)
}

func rewriteCursorResponsesModel(body []byte, clientModel string) []byte {
	clientModel = strings.TrimSpace(clientModel)
	if len(body) == 0 || clientModel == "" {
		return body
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	payload["model"] = clientModel
	patched, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return patched
}

func writeCursorCompatCapturedResponse(c *gin.Context, result *executorcompat.ExecuteResult, body []byte) {
	if c == nil || result == nil {
		return
	}
	status := result.StatusCode
	if status <= 0 {
		status = http.StatusOK
	}
	for key, values := range result.Header {
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
	if len(body) > 0 && strings.TrimSpace(c.Writer.Header().Get("Content-Type")) == "" {
		c.Writer.Header().Set("Content-Type", "application/json")
	}
	c.Status(status)
	if len(body) > 0 {
		_, _ = c.Writer.Write(body)
	}
}

func normalizeCursorResponsesError(result *executorcompat.ExecuteResult) (int, string) {
	if result == nil {
		return http.StatusBadGateway, "Compat gateway execution failed"
	}
	status := result.StatusCode
	if status <= 0 {
		status = http.StatusBadGateway
	}
	message := strings.TrimSpace(extractCursorCompatErrorMessage(result.Body))
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = "Compat gateway execution failed"
	}
	return status, message
}

func buildCompatCaptureTooLargeMessage(result *executorcompat.ExecuteResult) string {
	if result == nil || result.CaptureLimit <= 0 {
		return "Compat captured response exceeded buffer limit"
	}
	return fmt.Sprintf("Compat captured response exceeded buffer limit (%d bytes)", result.CaptureLimit)
}

func writeCursorCompatAnthropicError(c *gin.Context, result *executorcompat.ExecuteResult) {
	if c == nil {
		return
	}
	status := http.StatusBadGateway
	if result != nil && result.StatusCode > 0 {
		status = result.StatusCode
	}
	errType, message, code := extractCursorCompatAnthropicError(result.Body)
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = "Compat gateway execution failed"
	}
	if errType == "" {
		errType = "invalid_request_error"
	}
	payload := gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	}
	if strings.TrimSpace(code) != "" {
		payload["error"].(gin.H)["code"] = code
	}
	c.JSON(status, payload)
}

func extractCursorCompatAnthropicError(body []byte) (errType, message, code string) {
	if len(body) == 0 {
		return "", "", ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", strings.TrimSpace(string(body)), ""
	}
	errorObj, _ := payload["error"].(map[string]any)
	errType = strings.TrimSpace(stringValue(errorObj["type"]))
	if errType == "" {
		errType = strings.TrimSpace(stringValue(payload["type"]))
		if errType == "error" {
			errType = ""
		}
	}
	message = strings.TrimSpace(stringValue(errorObj["message"]))
	if message == "" {
		message = strings.TrimSpace(stringValue(payload["message"]))
	}
	code = strings.TrimSpace(stringValue(errorObj["code"]))
	if code == "" {
		code = strings.TrimSpace(stringValue(payload["code"]))
	}
	if errType == "" && code != "" {
		errType = code
	}
	return errType, message, code
}

func extractCursorCompatErrorMessage(body []byte) string {
	return extractCompatErrorMessage(body)
}

func extractCompatErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return strings.TrimSpace(string(body))
	}
	errorObj, _ := payload["error"].(map[string]any)
	if msg := strings.TrimSpace(stringValue(errorObj["message"])); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(stringValue(payload["message"])); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(stringValue(payload["error"])); msg != "" {
		return msg
	}
	return ""
}

func (h *CursorCompatHandler) streamCursorResponses(c *gin.Context, body []byte, action func(*gin.Context)) error {
	if c == nil || action == nil {
		return fmt.Errorf("compat gateway execution failed")
	}
	writer := newCursorResponsesStreamWriter(c.Writer, cursorRequestModel(body), getCompatGroupPlatform(c) == service.PlatformOpenAI)
	inner := cloneCompatStreamContext(c, writer, body)
	action(inner)
	syncCompatContextState(c, inner)
	return writer.Finish()
}

func (h *CursorCompatHandler) streamCursorMessages(c *gin.Context, body []byte, action func(*gin.Context)) error {
	if c == nil || action == nil {
		return fmt.Errorf("compat gateway execution failed")
	}
	writer := newCursorMessagesStreamWriter(c.Writer)
	inner := cloneCompatStreamContext(c, writer, body)
	action(inner)
	syncCompatContextState(c, inner)
	return writer.Finish()
}

func (h *AugmentCompatHandler) streamAugmentChat(c *gin.Context, body []byte, action func(*gin.Context)) error {
	if c == nil || action == nil {
		return fmt.Errorf("compat gateway execution failed")
	}
	writer := newAugmentChatStreamWriter(c.Writer)
	inner := cloneCompatStreamContext(c, writer, body)
	action(inner)
	syncCompatContextState(c, inner)
	writer.headers.Set("Content-Type", "application/x-ndjson")
	writer.headers.Set("Cache-Control", "no-cache")
	writer.headers.Set("Connection", "keep-alive")
	return writer.Finish()
}

func cloneCompatStreamContext(c *gin.Context, writer gin.ResponseWriter, body []byte) *gin.Context {
	inner := c.Copy()
	inner.Writer = writer
	if inner.Request == nil {
		inner.Request, _ = http.NewRequest(http.MethodPost, "/", nil)
	}
	if body != nil {
		inner.Request.Body = io.NopCloser(bytes.NewReader(body))
		inner.Request.ContentLength = int64(len(body))
	}
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

type streamChunkPatcher interface {
	PatchChunk(chunk []byte) ([]byte, error)
	Finish() ([]byte, error)
}

type compatStreamingWriter struct {
	base        gin.ResponseWriter
	headers     http.Header
	status      int
	size        int
	wroteHeader bool
	chunkBuf    bytes.Buffer
	patcher     streamChunkPatcher
	writeErr    error
}

func newCompatStreamingWriter(base gin.ResponseWriter, patcher streamChunkPatcher) *compatStreamingWriter {
	if base == nil {
		return nil
	}
	return &compatStreamingWriter{
		base:    base,
		headers: base.Header(),
		status:  http.StatusOK,
		size:    -1,
		patcher: patcher,
	}
}

func (w *compatStreamingWriter) Header() http.Header { return w.headers }
func (w *compatStreamingWriter) WriteHeader(code int) {
	if code > 0 && !w.Written() {
		w.status = code
	}
}
func (w *compatStreamingWriter) WriteHeaderNow() {
	if w.Written() {
		return
	}
	w.base.WriteHeader(w.status)
	w.size = 0
	w.wroteHeader = true
}
func (w *compatStreamingWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return len(p), w.writeErr
	}
	if len(p) == 0 {
		w.WriteHeaderNow()
		return 0, nil
	}
	w.chunkBuf.Write(p)
	for {
		chunk, ok := nextCompatSSEChunk(&w.chunkBuf)
		if !ok {
			break
		}
		patched, err := w.patchChunk(chunk)
		if err != nil {
			w.writeErr = err
			return len(p), err
		}
		if err := w.writePatched(patched); err != nil {
			w.writeErr = err
			return len(p), err
		}
	}
	return len(p), nil
}
func (w *compatStreamingWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *compatStreamingWriter) Status() int                       { return w.status }
func (w *compatStreamingWriter) Size() int                         { return w.size }
func (w *compatStreamingWriter) Written() bool                     { return w.size >= 0 }
func (w *compatStreamingWriter) Flush() {
	w.WriteHeaderNow()
	w.base.Flush()
}
func (w *compatStreamingWriter) CloseNotify() <-chan bool { return w.base.CloseNotify() }
func (w *compatStreamingWriter) Pusher() http.Pusher      { return w.base.Pusher() }
func (w *compatStreamingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.base.Hijack()
}
func (w *compatStreamingWriter) Finish() error {
	if w == nil {
		return nil
	}
	if w.writeErr != nil {
		return w.writeErr
	}
	if w.chunkBuf.Len() > 0 {
		patched, err := w.patchChunk(append([]byte(nil), w.chunkBuf.Bytes()...))
		if err != nil {
			w.writeErr = err
			return err
		}
		if err := w.writePatched(patched); err != nil {
			w.writeErr = err
			return err
		}
		w.chunkBuf.Reset()
	}
	if w.patcher != nil {
		tail, err := w.patcher.Finish()
		if err != nil {
			w.writeErr = err
			return err
		}
		if err := w.writePatched(tail); err != nil {
			w.writeErr = err
			return err
		}
	}
	w.Flush()
	return nil
}
func (w *compatStreamingWriter) patchChunk(chunk []byte) ([]byte, error) {
	if w.patcher == nil {
		return chunk, nil
	}
	return w.patcher.PatchChunk(chunk)
}
func (w *compatStreamingWriter) writePatched(patched []byte) error {
	if len(patched) == 0 {
		return nil
	}
	w.WriteHeaderNow()
	n, err := w.base.Write(patched)
	w.size += n
	if err == nil {
		w.base.Flush()
	}
	return err
}

func nextCompatSSEChunk(buf *bytes.Buffer) ([]byte, bool) {
	if buf == nil || buf.Len() == 0 {
		return nil, false
	}
	data := buf.Bytes()
	if idx := bytes.Index(data, []byte("\r\n\r\n")); idx >= 0 {
		chunk := append([]byte(nil), data[:idx+4]...)
		buf.Next(idx + 4)
		return chunk, true
	}
	if idx := bytes.Index(data, []byte("\n\n")); idx >= 0 {
		chunk := append([]byte(nil), data[:idx+2]...)
		buf.Next(idx + 2)
		return chunk, true
	}
	return nil, false
}

type cursorMessagesChunkPatcher struct {
	state *cursorcompat.MessagesStreamState
}

func newCursorMessagesStreamWriter(base gin.ResponseWriter) *compatStreamingWriter {
	return newCompatStreamingWriter(base, &cursorMessagesChunkPatcher{state: cursorcompat.NewMessagesStreamState()})
}

func (p *cursorMessagesChunkPatcher) PatchChunk(chunk []byte) ([]byte, error) {
	return cursorcompat.PatchMessagesStreamChunk(chunk, p.state)
}

func (p *cursorMessagesChunkPatcher) Finish() ([]byte, error) {
	return cursorcompat.FinalizeMessagesStream(p.state), nil
}

type cursorResponsesChunkPatcher struct {
	state       *cursorcompat.ResponsesStreamState
	clientModel string
}

type openAIResponsesPassthroughPatcher struct {
	clientModel string
}

func newCursorResponsesStreamWriter(base gin.ResponseWriter, clientModel string, passthrough bool) *compatStreamingWriter {
	if passthrough {
		return newCompatStreamingWriter(base, &openAIResponsesPassthroughPatcher{clientModel: clientModel})
	}
	return newCompatStreamingWriter(base, &cursorResponsesChunkPatcher{state: cursorcompat.NewResponsesStreamState(), clientModel: clientModel})
}

func (p *openAIResponsesPassthroughPatcher) PatchChunk(chunk []byte) ([]byte, error) {
	if len(chunk) == 0 || strings.TrimSpace(p.clientModel) == "" {
		return chunk, nil
	}
	eventName, data, ok := parseCompatSSEChunk(chunk)
	if !ok || strings.TrimSpace(data) == "" || strings.TrimSpace(data) == "[DONE]" {
		return chunk, nil
	}
	if eventName == "" {
		return chunk, nil
	}
	if eventName != "response.created" && eventName != "response.completed" && eventName != "response.incomplete" && eventName != "response.failed" {
		return chunk, nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return chunk, nil
	}
	response, ok := payload["response"].(map[string]any)
	if !ok {
		return chunk, nil
	}
	response["model"] = p.clientModel
	patched, err := json.Marshal(payload)
	if err != nil {
		return chunk, nil
	}
	var out strings.Builder
	lines := strings.Split(strings.ReplaceAll(string(chunk), "\r\n", "\n"), "\n")
	dataWritten := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "data:") && !dataWritten:
			out.WriteString("data: ")
			out.Write(patched)
			out.WriteString("\n")
			dataWritten = true
		case strings.HasPrefix(line, "data:"):
			continue
		default:
			out.WriteString(line)
			out.WriteString("\n")
		}
	}
	return []byte(out.String()), nil
}

func (p *openAIResponsesPassthroughPatcher) Finish() ([]byte, error) {
	return nil, nil
}

func (p *cursorResponsesChunkPatcher) PatchChunk(chunk []byte) ([]byte, error) {
	return cursorcompat.PatchResponsesStreamChunk(chunk, p.clientModel, p.state)
}

func (p *cursorResponsesChunkPatcher) Finish() ([]byte, error) {
	return nil, nil
}

type augmentChatChunkPatcher struct {
	nextNodeID      int
	sawToolUse      bool
	stopReasonSeen  bool
	stopReason      int
	usage           map[string]interface{}
	thinkingStates  map[int]*augmentThinkingStateCompat
	toolID          string
	toolName        string
	toolInput       strings.Builder
	toolActive      bool
	endedWithStop   bool
	renderedPayload bool
}

type augmentThinkingStateCompat struct {
	summary   string
	signature string
}

func newAugmentChatStreamWriter(base gin.ResponseWriter) *compatStreamingWriter {
	writer := newCompatStreamingWriter(base, &augmentChatChunkPatcher{
		nextNodeID:     1,
		stopReason:     1,
		usage:          map[string]interface{}{},
		thinkingStates: map[int]*augmentThinkingStateCompat{},
	})
	writer.headers.Set("Content-Type", "application/x-ndjson")
	writer.headers.Set("Cache-Control", "no-cache")
	writer.headers.Set("Connection", "keep-alive")
	return writer
}

func newAugmentOpenAIResponsesStreamWriter(base gin.ResponseWriter, toolMetaByName map[string]augmentcompat.ToolMetadata, supportToolUseStart bool) *compatStreamingWriter {
	writer := newCompatStreamingWriter(base, augmentcompat.NewOpenAIResponsesStreamPatcher(augmentcompat.OpenAIResponsesConvertOptions{
		ToolMetaByName:      toolMetaByName,
		SupportToolUseStart: supportToolUseStart,
	}))
	writer.headers.Set("Content-Type", "application/x-ndjson")
	writer.headers.Set("Cache-Control", "no-cache")
	writer.headers.Set("Connection", "keep-alive")
	return writer
}

func (p *augmentChatChunkPatcher) PatchChunk(chunk []byte) ([]byte, error) {
	var out bytes.Buffer
	eventType, data, ok := parseCompatSSEChunk(chunk)
	if !ok {
		raw := bytes.TrimSpace(chunk)
		if len(raw) == 0 || (!bytes.HasPrefix(raw, []byte("{")) && !bytes.HasPrefix(raw, []byte("["))) {
			return nil, nil
		}
		if _, _, _, err := augmentcompat.ConvertJSONToNDJSON(raw, &out); err != nil {
			return nil, err
		}
		p.renderedPayload = true
		p.endedWithStop = true
		return out.Bytes(), nil
	}
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		if data == "[DONE]" {
			p.endedWithStop = true
		}
		return nil, nil
	}
	var ev map[string]interface{}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil, nil
	}
	if _, ok := ev["type"].(string); !ok && eventType != "" {
		ev["type"] = eventType
	}
	typ := stringValue(ev["type"])
	if typ == "error" || firstMap(ev, "error") != nil {
		return nil, fmt.Errorf("augment response: claude upstream error: %s", extractUpstreamErrorMessage(ev))
	}
	switch typ {
	case "content_block_start":
		cb := firstMap(ev, "content_block")
		if stringValue(cb["type"]) == "tool_use" {
			p.toolActive = true
			p.toolID = stringValue(cb["id"])
			p.toolName = stringValue(cb["name"])
			p.toolInput.Reset()
		}
	case "content_block_delta":
		delta := firstMap(ev, "delta")
		switch stringValue(delta["type"]) {
		case "text_delta":
			text := stringValue(delta["text"])
			if text != "" {
				augmentcompatWriteChunkLine(&out, augmentcompatNewBaseChunk(text))
			}
		case "input_json_delta":
			if p.toolActive {
				p.toolInput.WriteString(stringValue(delta["partial_json"]))
			}
		case "thinking_delta":
			thinking := stringValue(delta["thinking"])
			if strings.TrimSpace(thinking) != "" {
				idx := intValue(ev["index"])
				state := p.thinkingStates[idx]
				if state == nil {
					state = &augmentThinkingStateCompat{}
					p.thinkingStates[idx] = state
				}
				state.summary += thinking
				augmentcompatEmitThinkingChunk(&out, state.summary, state.signature, &p.nextNodeID)
			}
		case "signature_delta":
			signature := stringValue(delta["signature"])
			if strings.TrimSpace(signature) != "" {
				idx := intValue(ev["index"])
				state := p.thinkingStates[idx]
				if state == nil {
					state = &augmentThinkingStateCompat{}
					p.thinkingStates[idx] = state
				}
				state.signature += signature
				augmentcompatEmitThinkingChunk(&out, state.summary, state.signature, &p.nextNodeID)
			}
		}
	case "content_block_stop":
		if p.toolActive {
			if augmentcompatEmitToolUseChunks(&out, p.toolID, p.toolName, p.toolInput.String(), &p.nextNodeID) {
				p.sawToolUse = true
			}
			p.toolActive = false
		}
	case "message_start":
		mergeUsage(p.usage, firstMap(ev, "usage"))
		mergeUsage(p.usage, firstMap(firstMap(ev, "message"), "usage"))
	case "message_delta":
		delta := firstMap(ev, "delta")
		if sr := stringValue(delta["stop_reason"]); sr != "" {
			p.stopReasonSeen = true
			p.stopReason = mapClaudeStopReason(sr)
		}
		mergeUsage(p.usage, firstMap(ev, "usage"))
	case "message_stop":
		p.endedWithStop = true
		mergeUsage(p.usage, firstMap(ev, "usage"))
	}
	return out.Bytes(), nil
}

func (p *augmentChatChunkPatcher) Finish() ([]byte, error) {
	if p.renderedPayload {
		return nil, nil
	}
	var out bytes.Buffer
	if p.toolActive {
		if augmentcompatEmitToolUseChunks(&out, p.toolID, p.toolName, p.toolInput.String(), &p.nextNodeID) {
			p.sawToolUse = true
		}
		p.toolActive = false
	}
	augmentcompatEmitTokenUsageChunk(&out, p.usage, &p.nextNodeID)
	augmentcompatEmitFinalStopChunk(&out, p.stopReasonSeen, p.stopReason, p.sawToolUse, p.endedWithStop)
	return out.Bytes(), nil
}

func parseCompatSSEChunk(chunk []byte) (string, string, bool) {
	if len(chunk) == 0 {
		return "", "", false
	}
	lines := strings.Split(strings.ReplaceAll(string(chunk), "\r\n", "\n"), "\n")
	eventName := ""
	dataLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if len(dataLines) == 0 {
		return "", "", false
	}
	return eventName, strings.Join(dataLines, "\n"), true
}

func firstMap(m map[string]interface{}, key string) map[string]interface{} {
	if raw, ok := m[key].(map[string]interface{}); ok {
		return raw
	}
	return nil
}

func extractUpstreamErrorMessage(obj map[string]interface{}) string {
	if errObj := firstMap(obj, "error"); len(errObj) > 0 {
		if msg := stringValue(errObj["message"]); msg != "" {
			return msg
		}
	}
	if msg := stringValue(obj["message"]); msg != "" {
		return msg
	}
	return "claude upstream error"
}

func mapClaudeStopReason(sr string) int {
	switch strings.ToLower(strings.TrimSpace(sr)) {
	case "end_turn", "stop_sequence":
		return 1
	case "max_tokens":
		return 2
	case "tool_use":
		return 3
	case "safety":
		return 4
	case "recitation":
		return 5
	default:
		return 1
	}
}

func mergeUsage(dst map[string]interface{}, src map[string]interface{}) {
	if len(src) == 0 {
		return
	}
	for _, key := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		if v, ok := usageInt(src, key); ok {
			dst[key] = v
		}
	}
}

func usageInt(m map[string]interface{}, key string) (int, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case int:
		return t, true
	case int32:
		return int(t), true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	default:
		return 0, false
	}
}

func intValue(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

func augmentcompatWriteChunkLine(w io.Writer, obj map[string]interface{}) {
	line, err := json.Marshal(obj)
	if err != nil {
		_, _ = io.WriteString(w, "{\"error\":\"json_marshal_failed\"}\n")
		return
	}
	_, _ = w.Write(line)
	_, _ = w.Write([]byte("\n"))
}

func augmentcompatNewBaseChunk(text string) map[string]interface{} {
	return map[string]interface{}{
		"text":                  text,
		"unknown_blob_names":    []interface{}{},
		"checkpoint_not_found":  false,
		"workspace_file_chunks": []interface{}{},
		"nodes":                 []interface{}{},
	}
}

func augmentcompatEmitThinkingChunk(w io.Writer, text, signature string, nextNodeID *int) {
	if strings.TrimSpace(text) == "" && strings.TrimSpace(signature) == "" {
		return
	}
	node := map[string]interface{}{
		"id":      *nextNodeID,
		"type":    8,
		"content": "",
		"thinking": map[string]interface{}{
			"summary":   text,
			"signature": signature,
		},
	}
	*nextNodeID++
	chunk := augmentcompatNewBaseChunk("")
	chunk["nodes"] = []interface{}{node}
	augmentcompatWriteChunkLine(w, chunk)
}

func augmentcompatEmitToolUseChunks(w io.Writer, toolUseID, toolName, inputJSON string, nextNodeID *int) bool {
	if strings.TrimSpace(toolName) == "" {
		return false
	}
	if strings.TrimSpace(toolUseID) == "" {
		toolUseID = fmt.Sprintf("tool-%d", *nextNodeID)
	}
	if strings.TrimSpace(inputJSON) == "" {
		inputJSON = "{}"
	}
	toolUse := map[string]interface{}{
		"tool_name":   toolName,
		"tool_use_id": toolUseID,
		"input_json":  inputJSON,
	}
	startNode := map[string]interface{}{"id": *nextNodeID, "type": 7, "content": "", "tool_use": toolUse}
	*nextNodeID++
	startChunk := augmentcompatNewBaseChunk("")
	startChunk["nodes"] = []interface{}{startNode}
	augmentcompatWriteChunkLine(w, startChunk)
	node := map[string]interface{}{"id": *nextNodeID, "type": 5, "content": "", "tool_use": toolUse}
	*nextNodeID++
	chunk := augmentcompatNewBaseChunk("")
	chunk["nodes"] = []interface{}{node}
	augmentcompatWriteChunkLine(w, chunk)
	return true
}

func augmentcompatEmitTokenUsageChunk(w io.Writer, tokenUsage map[string]interface{}, nextNodeID *int) bool {
	out := map[string]interface{}{}
	for _, key := range []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		if v, ok := usageInt(tokenUsage, key); ok {
			out[key] = v
		}
	}
	if len(out) == 0 {
		return false
	}
	node := map[string]interface{}{"id": *nextNodeID, "type": 10, "content": "", "token_usage": out}
	*nextNodeID++
	chunk := augmentcompatNewBaseChunk("")
	chunk["nodes"] = []interface{}{node}
	augmentcompatWriteChunkLine(w, chunk)
	return true
}

func augmentcompatEmitFinalStopChunk(w io.Writer, stopReasonSeen bool, stopReason int, sawToolUse bool, endedCleanly bool) {
	finalReason := 0
	if endedCleanly {
		if stopReasonSeen {
			finalReason = stopReason
		} else if sawToolUse {
			finalReason = 3
		} else {
			finalReason = 1
		}
	}
	chunk := augmentcompatNewBaseChunk("")
	chunk["stop_reason"] = finalReason
	augmentcompatWriteChunkLine(w, chunk)
}

func cursorCompatError(c *gin.Context, status int, message string) {
	cursorCompatTypedError(c, status, "invalid_request_error", message)
}

func cursorCompatTypedError(c *gin.Context, status int, errType string, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
		},
	})
}

func applyCursorResponsesStreamCompat(c *gin.Context) {
	setCompatStreamPingFormat(c, SSEPingFormatComment)
	setCompatStreamErrorFormat(c, compatStreamErrorFormatResponsesEvent)
}

func applyCursorChatCompletionsStreamCompat(c *gin.Context) {
	setCompatStreamPingFormat(c, SSEPingFormatComment)
	setCompatStreamErrorFormat(c, compatStreamErrorFormatOpenAIEvent)
}

func applyCursorMessagesStreamCompat(c *gin.Context) {
	setCompatStreamPingFormat(c, SSEPingFormatClaude)
	setCompatStreamErrorFormat(c, compatStreamErrorFormatAnthropicEvent)
}

type AugmentCompatHandler struct {
	gateway               *GatewayHandler
	openaiGateway         *OpenAIGatewayHandler
	facade                *executorcompat.Facade
	messagesAction        func(*gin.Context)
	openaiResponsesAction func(*gin.Context)
	modelsAction          func(*gin.Context)
}

func NewAugmentCompatHandler(gatewayHandler *GatewayHandler, openaiGatewayHandler *OpenAIGatewayHandler) *AugmentCompatHandler {
	h := &AugmentCompatHandler{
		gateway:       gatewayHandler,
		openaiGateway: openaiGatewayHandler,
		facade:        executorcompat.New(4 * 1024 * 1024),
	}
	if gatewayHandler != nil {
		h.messagesAction = gatewayHandler.Messages
		h.modelsAction = gatewayHandler.Models
	}
	if openaiGatewayHandler != nil {
		h.openaiResponsesAction = openaiGatewayHandler.Responses
	}
	return h
}

func (h *AugmentCompatHandler) ChatStream(c *gin.Context) {
	if h == nil {
		augmentCompatStreamError(c, http.StatusOK, "Compat gateway is unavailable")
		return
	}
	body, err := readCompatRequestBody(c)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			augmentCompatStreamError(c, http.StatusOK, buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		augmentCompatStreamError(c, http.StatusOK, "Failed to read request body")
		return
	}
	if len(body) == 0 {
		augmentCompatStreamError(c, http.StatusOK, "Request body is empty")
		return
	}
	if _, ok := middleware2.GetAPIKeyFromContext(c); !ok {
		augmentCompatStreamError(c, http.StatusOK, "Invalid API key")
		return
	}

	groupPlatform := getCompatGroupPlatform(c)
	switch groupPlatform {
	case service.PlatformAnthropic:
		if h.messagesAction == nil {
			augmentCompatStreamError(c, http.StatusOK, "Compat gateway is unavailable")
			return
		}
		normalized, stream, err := augmentcompat.NormalizeChatStreamRequest(body)
		if err != nil {
			augmentCompatStreamError(c, http.StatusOK, fmt.Sprintf("Failed to normalize request body: %v", err))
			return
		}
		if !stream {
			result, execErr := h.compatFacade().ExecuteMessages(c, normalized, h.messagesAction)
			if execErr != nil || result == nil {
				augmentCompatStreamError(c, http.StatusOK, "Compat gateway execution failed")
				return
			}
			if result.BodyTruncated {
				augmentCompatStreamError(c, http.StatusOK, buildCompatCaptureTooLargeMessage(result))
				return
			}
			if err := writeAugmentNDJSONResponse(c, result); err != nil {
				augmentCompatStreamError(c, http.StatusOK, normalizeAugmentRenderError(err))
			}
			return
		}
		if err := h.streamAugmentChat(c, normalized, h.messagesAction); err != nil {
			augmentCompatStreamError(c, http.StatusOK, normalizeAugmentRenderError(err))
		}
	case service.PlatformOpenAI:
		if h.openaiResponsesAction == nil {
			augmentCompatStreamError(c, http.StatusOK, "Compat gateway is unavailable")
			return
		}
		prepared, err := augmentcompat.PrepareChatStreamOpenAIResponsesRequest(body)
		if err != nil {
			augmentCompatStreamError(c, http.StatusOK, fmt.Sprintf("Failed to normalize request body: %v", err))
			return
		}
		if !prepared.Stream {
			result, execErr := h.compatFacade().ExecuteMessages(c, prepared.Body, h.openaiResponsesAction)
			if execErr != nil || result == nil {
				augmentCompatStreamError(c, http.StatusOK, "Compat gateway execution failed")
				return
			}
			if result.BodyTruncated {
				augmentCompatStreamError(c, http.StatusOK, buildCompatCaptureTooLargeMessage(result))
				return
			}
			if err := writeAugmentOpenAIResponsesNDJSON(c, result, prepared.ToolMetaByName, prepared.SupportToolUseStart); err != nil {
				augmentCompatStreamError(c, http.StatusOK, normalizeAugmentRenderError(err))
			}
			return
		}
		if err := h.streamAugmentOpenAIResponses(c, prepared.Body, prepared.ToolMetaByName, prepared.SupportToolUseStart, h.openaiResponsesAction); err != nil {
			augmentCompatStreamError(c, http.StatusOK, normalizeAugmentRenderError(err))
		}
	default:
		augmentCompatStreamError(c, http.StatusOK, "Augment chat-stream only supports Anthropic-compatible or OpenAI-compatible groups")
		return
	}
}

func (h *AugmentCompatHandler) streamAugmentOpenAIResponses(c *gin.Context, body []byte, toolMetaByName map[string]augmentcompat.ToolMetadata, supportToolUseStart bool, action func(*gin.Context)) error {
	if c == nil || action == nil {
		return fmt.Errorf("compat gateway execution failed")
	}
	writer := newAugmentOpenAIResponsesStreamWriter(c.Writer, toolMetaByName, supportToolUseStart)
	inner := cloneCompatStreamContext(c, writer, body)
	action(inner)
	syncCompatContextState(c, inner)
	writer.headers.Set("Content-Type", "application/x-ndjson")
	writer.headers.Set("Cache-Control", "no-cache")
	writer.headers.Set("Connection", "keep-alive")
	return writer.Finish()
}

func writeAugmentOpenAIResponsesNDJSON(c *gin.Context, result *executorcompat.ExecuteResult, toolMetaByName map[string]augmentcompat.ToolMetadata, supportToolUseStart bool) error {
	if c == nil || result == nil {
		return fmt.Errorf("empty compat result")
	}
	var buf bytes.Buffer
	contentType := strings.ToLower(strings.TrimSpace(result.Header.Get("Content-Type")))
	options := augmentcompat.OpenAIResponsesConvertOptions{
		ToolMetaByName:      toolMetaByName,
		SupportToolUseStart: supportToolUseStart,
	}
	if strings.Contains(contentType, "text/event-stream") {
		_, _, _, err := augmentcompat.StreamConvertOpenAIResponsesSSEToNDJSON(bytes.NewReader(result.Body), &buf, options)
		if err != nil {
			return err
		}
	} else {
		_, _, _, err := augmentcompat.ConvertOpenAIResponsesJSONToNDJSON(result.Body, &buf, options)
		if err != nil {
			return err
		}
	}
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	status := result.StatusCode
	if status <= 0 {
		status = http.StatusOK
	}
	c.Status(status)
	_, _ = c.Writer.Write(buf.Bytes())
	return nil
}

func (h *AugmentCompatHandler) GetModels(c *gin.Context) {
	if h == nil || h.modelsAction == nil {
		augmentCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}
	result, err := h.compatFacade().ExecuteMessages(c, nil, h.modelsAction)
	if err != nil || result == nil {
		augmentCompatError(c, http.StatusBadGateway, "Compat gateway execution failed")
		return
	}
	if result.BodyTruncated {
		augmentCompatError(c, http.StatusBadGateway, buildCompatCaptureTooLargeMessage(result))
		return
	}
	if result.StatusCode >= http.StatusBadRequest {
		status := result.StatusCode
		if status <= 0 {
			status = http.StatusBadGateway
		}
		message := extractCompatErrorMessage(result.Body)
		if message == "" {
			message = http.StatusText(status)
		}
		if message == "" {
			message = "Compat gateway execution failed"
		}
		augmentCompatError(c, status, message)
		return
	}
	models, convErr := transformAugmentModels(result.Body)
	if convErr != nil {
		augmentCompatError(c, http.StatusBadGateway, "Failed to render model list")
		return
	}
	c.JSON(http.StatusOK, models)
}

func (h *AugmentCompatHandler) GetBalance(c *gin.Context) {
	apiKey, _ := middleware2.GetAPIKeyFromContext(c)
	quotaRemaining := float64(0)
	unlimited := true
	expiredTime := int64(4102444800)
	statusText := "enabled"
	status := 1
	remainAmount := float64(0)
	if apiKey != nil {
		if apiKey.Quota > 0 {
			quotaRemaining = apiKey.GetQuotaRemaining()
			unlimited = false
		}
		if apiKey.ExpiresAt != nil {
			expiredTime = apiKey.ExpiresAt.Unix()
		}
		switch apiKey.Status {
		case service.StatusAPIKeyDisabled:
			status = 0
			statusText = "disabled"
		case service.StatusAPIKeyExpired:
			status = 0
			statusText = "expired"
		case service.StatusAPIKeyQuotaExhausted:
			status = 0
			statusText = "quota_exhausted"
		}
		if apiKey.User != nil {
			remainAmount = apiKey.User.Balance
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"name":          "Sub2API",
			"remain_quota":  quotaRemaining,
			"remain_amount": remainAmount,
			"unlimited":     unlimited,
			"expired_time":  expiredTime,
			"status":        status,
			"status_text":   statusText,
		},
	})
}

func (h *AugmentCompatHandler) GetLoginToken(c *gin.Context) {
	apiKey, _ := middleware2.GetAPIKeyFromContext(c)
	accessToken := ""
	if apiKey != nil {
		accessToken = strings.TrimSpace(apiKey.Key)
	}
	tenantURL := deriveAugmentTenantURL(c)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tenantUrl":   tenantURL,
			"accessToken": accessToken,
		},
		"tenantUrl":   tenantURL,
		"accessToken": accessToken,
	})
}

func augmentCompatError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{
		"success": false,
		"error":   message,
	})
}

func augmentCompatStreamError(c *gin.Context, status int, message string) {
	if c == nil {
		return
	}
	if status <= 0 {
		status = http.StatusOK
	}
	payload, _ := json.Marshal(gin.H{
		"type": "error",
		"error": gin.H{
			"message": message,
		},
	})
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(status)
	_, _ = c.Writer.Write(payload)
	_, _ = c.Writer.Write([]byte("\n"))
}

func writeAugmentNDJSONResponse(c *gin.Context, result *executorcompat.ExecuteResult) error {
	if c == nil || result == nil {
		return fmt.Errorf("empty compat result")
	}
	var buf bytes.Buffer
	contentType := strings.ToLower(strings.TrimSpace(result.Header.Get("Content-Type")))
	if strings.Contains(contentType, "text/event-stream") {
		_, _, _, err := augmentcompat.StreamConvertSSEToNDJSON(bytes.NewReader(result.Body), &buf)
		if err != nil {
			return err
		}
	} else {
		_, _, _, err := augmentcompat.ConvertJSONToNDJSON(result.Body, &buf)
		if err != nil {
			return err
		}
	}
	status := result.StatusCode
	if status <= 0 {
		status = http.StatusOK
	}
	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(status)
	_, _ = c.Writer.Write(buf.Bytes())
	return nil
}

func normalizeAugmentRenderError(err error) string {
	if err == nil {
		return "augment render failed"
	}
	msg := strings.TrimSpace(err.Error())
	for _, prefix := range []string{
		"augment response: OpenAI upstream error:",
		"augment response: claude upstream error:",
		"augment response:",
		"OpenAI upstream error:",
	} {
		msg = strings.TrimSpace(strings.TrimPrefix(msg, prefix))
	}
	if msg == "" {
		return "augment render failed"
	}
	return msg
}

func transformAugmentModels(body []byte) (map[string]gin.H, error) {
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	models := make(map[string]gin.H, len(payload.Data))
	for i, item := range payload.Data {
		id := strings.TrimSpace(stringValue(item["id"]))
		if id == "" {
			continue
		}
		description := strings.TrimSpace(stringValue(item["display_name"]))
		if description == "" {
			description = strings.TrimSpace(stringValue(item["displayName"]))
		}
		if description == "" {
			description = id
		}
		shortName := id
		if dash := strings.Index(shortName, "-"); dash > 0 {
			shortName = shortName[:dash]
		}
		models[id] = gin.H{
			"displayName":   id,
			"description":   description,
			"shortName":     shortName,
			"priority":      i + 1,
			"isLegacyModel": false,
		}
	}
	return models, nil
}

func (h *AugmentCompatHandler) compatFacade() *executorcompat.Facade {
	if h == nil {
		return executorcompat.New(4 * 1024 * 1024)
	}
	if h.facade == nil {
		h.facade = executorcompat.New(4 * 1024 * 1024)
	}
	return h.facade
}

func readCompatRequestBody(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	return httputil.ReadRequestBodyWithPrealloc(c.Request)
}

func deriveAugmentTenantURL(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else if c.Request.URL != nil && strings.TrimSpace(c.Request.URL.Scheme) != "" {
			scheme = strings.TrimSpace(c.Request.URL.Scheme)
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(c.Request.Host)
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

func stringValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case fmt.Stringer:
		return t.String()
	default:
		return ""
	}
}

func rewriteRequestBody(c *gin.Context, body []byte) {
	if c == nil || c.Request == nil {
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
}

func getCompatGroupPlatform(c *gin.Context) string {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Platform
}
