package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	augmentcompat "github.com/Wei-Shaw/sub2api/internal/compat/augment"
	executorcompat "github.com/Wei-Shaw/sub2api/internal/compat/executor"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AugmentCompatHandler provides thin Augment-compatible entrypoints.
type AugmentCompatHandler struct {
	gateway               *GatewayHandler
	openaiGateway         *OpenAIGatewayHandler
	facade                *executorcompat.Facade
	maxRequestBodyBytes   int64
	messagesAction        func(*gin.Context)
	openaiResponsesAction func(*gin.Context)
	modelsAction          func(*gin.Context)
}

func NewAugmentCompatHandler(gatewayHandler *GatewayHandler, openaiGatewayHandler *OpenAIGatewayHandler, maxRequestBodyBytes int64) *AugmentCompatHandler {
	h := &AugmentCompatHandler{
		gateway:             gatewayHandler,
		openaiGateway:       openaiGatewayHandler,
		facade:              executorcompat.New(4 * 1024 * 1024),
		maxRequestBodyBytes: maxRequestBodyBytes,
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
	body, err := h.readCompatRequestBody(c)
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
		message := strings.TrimSpace(extractAugmentErrorMessage(result.Body))
		if message == "" {
			message = http.StatusText(result.StatusCode)
		}
		if message == "" {
			message = "Compat gateway execution failed"
		}
		augmentCompatError(c, result.StatusCode, message)
		return
	}
	models, err := transformAugmentModels(result.Body)
	if err != nil {
		augmentCompatError(c, http.StatusBadGateway, "Failed to render model list")
		return
	}
	c.JSON(http.StatusOK, models)
}

func (h *AugmentCompatHandler) Balance(c *gin.Context) {
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

// AugmentErrorWriter emits Augment compat errors for shared middleware.
func AugmentErrorWriter(c *gin.Context, status int, message string) {
	augmentCompatError(c, status, message)
}

// AugmentAuthErrorWriter emits Augment auth errors while preserving compat shape.
func AugmentAuthErrorWriter(c *gin.Context, status int, code, message string) {
	AugmentErrorWriter(c, status, message)
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

func (h *AugmentCompatHandler) streamAugmentOpenAIResponses(c *gin.Context, body []byte, toolMetaByName map[string]augmentcompat.ToolMetadata, supportToolUseStart bool, action func(*gin.Context)) error {
	if c == nil || action == nil {
		return fmt.Errorf("compat gateway execution failed")
	}
	inner := c.Copy()
	if inner.Request == nil {
		inner.Request, _ = http.NewRequest(http.MethodPost, "/", nil)
	}
	inner.Request.Body = io.NopCloser(bytes.NewReader(body))
	inner.Request.ContentLength = int64(len(body))
	rec := &captureAugmentResponseWriter{ResponseWriter: c.Writer, header: http.Header{}}
	inner.Writer = rec
	action(inner)

	result := &executorcompat.ExecuteResult{StatusCode: rec.status, Header: rec.header, Body: rec.body.Bytes()}
	return writeAugmentOpenAIResponsesNDJSON(c, result, toolMetaByName, supportToolUseStart)
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

func (h *AugmentCompatHandler) augmentMaxRequestBodyBytes() int64 {
	const defaultBytes = int64(8 << 20)
	if h != nil && h.maxRequestBodyBytes > 0 {
		return h.maxRequestBodyBytes
	}
	return defaultBytes
}

func (h *AugmentCompatHandler) readCompatRequestBody(c *gin.Context) ([]byte, error) {
	if c == nil || c.Request == nil {
		return nil, fmt.Errorf("request is nil")
	}
	return io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, h.augmentMaxRequestBodyBytes()))
}

func rewriteRequestBody(c *gin.Context, body []byte) {
	if c == nil || c.Request == nil {
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
}

func (h *AugmentCompatHandler) streamAugmentChat(c *gin.Context, body []byte, action func(*gin.Context)) error {
	if c == nil || action == nil {
		return fmt.Errorf("compat gateway execution failed")
	}
	inner := c.Copy()
	if inner.Request == nil {
		inner.Request, _ = http.NewRequest(http.MethodPost, "/", nil)
	}
	inner.Request.Body = io.NopCloser(bytes.NewReader(body))
	inner.Request.ContentLength = int64(len(body))
	rec := &captureAugmentResponseWriter{ResponseWriter: c.Writer, header: http.Header{}}
	inner.Writer = rec
	action(inner)

	result := &executorcompat.ExecuteResult{StatusCode: rec.status, Header: rec.header, Body: rec.body.Bytes()}
	return writeAugmentNDJSONResponse(c, result)
}

type captureAugmentResponseWriter struct {
	gin.ResponseWriter
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *captureAugmentResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}

func (w *captureAugmentResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
}

func (w *captureAugmentResponseWriter) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

func extractAugmentErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return strings.TrimSpace(string(body))
	}
	errorObj, _ := payload["error"].(map[string]any)
	if msg := strings.TrimSpace(augmentStringValue(errorObj["message"])); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(augmentStringValue(payload["message"])); msg != "" {
		return msg
	}
	if msg := strings.TrimSpace(augmentStringValue(payload["error"])); msg != "" {
		return msg
	}
	return ""
}

func deriveAugmentTenantURL(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
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

func transformAugmentModels(body []byte) (map[string]gin.H, error) {
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	models := make(map[string]gin.H, len(payload.Data))
	for i, item := range payload.Data {
		id := strings.TrimSpace(augmentStringValue(item["id"]))
		if id == "" {
			continue
		}
		description := strings.TrimSpace(augmentStringValue(item["display_name"]))
		if description == "" {
			description = strings.TrimSpace(augmentStringValue(item["displayName"]))
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
	if h != nil && h.facade != nil {
		return h.facade
	}
	return executorcompat.New(4 * 1024 * 1024)
}

func augmentStringValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return ""
	}
}
