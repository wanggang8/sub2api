package handler

import (
	"net/http"

	cursorcompat "github.com/Wei-Shaw/sub2api/internal/compat/cursor"
	executorcompat "github.com/Wei-Shaw/sub2api/internal/compat/executor"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// CursorCompatHandler provides a thin Cursor-compatible entry layer.
//
// Block 1 scope intentionally stays minimal: it only wires Cursor routes to the
// existing upstream handlers and enforces platform restrictions. Request
// normalization, capture execution, session compatibility, and stream patching
// are implemented in later blocks.
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
	}
	return h
}

func (h *CursorCompatHandler) Responses(c *gin.Context) {
	body, ok := normalizeCursorRequestBodyBytes(c, cursorcompat.NormalizeResponsesRequestBody)
	if !ok {
		return
	}
	stream := cursorCompatRequestStreamFromBody(body)
	requestModel := cursorCompatRequestModelFromBody(body)
	action := h.responsesAction
	if getCompatGroupPlatform(c) == service.PlatformOpenAI {
		action = h.openaiResponsesAction
	}
	if action == nil {
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}
	if stream {
		writer := newCursorResponsesStreamWriter(c.Writer, requestModel)
		c.Writer = writer
		action(c)
		return
	}
	result, err := h.compatFacade().ExecuteMessages(c, body, action)
	if err != nil || result == nil {
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway execution failed")
		return
	}
	if result.BodyTruncated {
		cursorCompatError(c, http.StatusBadGateway, buildCompatCaptureTooLargeMessage(result))
		return
	}
	if result.StatusCode >= http.StatusBadRequest {
		status, message := normalizeCursorCompatError(result)
		cursorCompatError(c, status, message)
		return
	}
	body = result.Body
	if getCompatGroupPlatform(c) == service.PlatformOpenAI {
		body = rewriteCursorCompatResponsesModel(body, requestModel)
	}
	writeCursorCompatCapturedResponse(c, result, body)
}

func (h *CursorCompatHandler) ChatCompletions(c *gin.Context) {
	body, ok := normalizeCursorRequestBodyBytes(c, cursorcompat.NormalizeChatCompletionsRequestBody)
	if !ok {
		return
	}
	platform := getCompatGroupPlatform(c)
	stream := cursorCompatRequestStreamFromBody(body)
	if platform == service.PlatformOpenAI {
		if h == nil || h.openaiChatCompletionsAction == nil {
			cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
			return
		}
		if stream {
			h.openaiChatCompletionsAction(c)
			return
		}
		result, err := h.compatFacade().ExecuteMessages(c, body, h.openaiChatCompletionsAction)
		if err != nil || result == nil {
			cursorCompatError(c, http.StatusBadGateway, "Compat gateway execution failed")
			return
		}
		if result.BodyTruncated {
			cursorCompatError(c, http.StatusBadGateway, buildCompatCaptureTooLargeMessage(result))
			return
		}
		if result.StatusCode >= http.StatusBadRequest {
			status, message := normalizeCursorCompatError(result)
			cursorCompatError(c, status, message)
			return
		}
		writeCursorCompatCapturedResponse(c, result, result.Body)
		return
	}
	if h == nil || h.chatCompletionsAction == nil {
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}
	if stream {
		h.chatCompletionsAction(c)
		return
	}
	result, err := h.compatFacade().ExecuteMessages(c, body, h.chatCompletionsAction)
	if err != nil || result == nil {
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway execution failed")
		return
	}
	if result.BodyTruncated {
		cursorCompatError(c, http.StatusBadGateway, buildCompatCaptureTooLargeMessage(result))
		return
	}
	if result.StatusCode >= http.StatusBadRequest {
		writeCursorCompatAnthropicError(c, result)
		return
	}
	writeCursorCompatCapturedResponse(c, result, result.Body)
}

func (h *CursorCompatHandler) Messages(c *gin.Context) {
	body, ok := normalizeCursorRequestBodyBytes(c, cursorcompat.NormalizeMessagesRequestBody)
	if !ok {
		return
	}
	// OpenAI-compatible Cursor messages is intentionally left out for now.
	// Non-messages Cursor OpenAI requests already reuse the OpenAI gateway
	// capability selection path, while this endpoint remains Anthropic-only.
	if getCompatGroupPlatform(c) != service.PlatformAnthropic {
		cursorCompatTypedError(c, http.StatusBadRequest, "invalid_request_error", "Cursor messages only supports Anthropic-compatible groups")
		return
	}
	if h == nil || h.messagesAction == nil {
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}
	if cursorCompatRequestStreamFromBody(body) {
		writer := newCursorMessagesStreamWriter(c.Writer)
		c.Writer = writer
		h.messagesAction(c)
		writer.Flush()
		return
	}
	result, err := h.compatFacade().ExecuteMessages(c, body, h.messagesAction)
	if err != nil || result == nil {
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway execution failed")
		return
	}
	if result.BodyTruncated {
		cursorCompatError(c, http.StatusBadGateway, buildCompatCaptureTooLargeMessage(result))
		return
	}
	if result.StatusCode >= http.StatusBadRequest {
		writeCursorCompatAnthropicError(c, result)
		return
	}
	body = patchCursorMessagesResponseBody(result.Body)
	writeCursorCompatCapturedResponse(c, result, body)
}

func (h *CursorCompatHandler) CountTokens(c *gin.Context) {
	if getCompatGroupPlatform(c) != service.PlatformAnthropic {
		cursorCompatTypedError(c, http.StatusNotFound, "not_found_error", "Token counting is not supported for this platform")
		return
	}
	body, ok := normalizeCursorRequestBodyBytes(c, cursorcompat.NormalizeMessagesRequestBody)
	if !ok {
		return
	}
	_ = body
	if h == nil || h.countTokensAction == nil {
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}
	h.countTokensAction(c)
}

func (h *CursorCompatHandler) Models(c *gin.Context) {
	if h == nil || h.modelsAction == nil {
		cursorCompatError(c, http.StatusBadGateway, "Compat gateway is unavailable")
		return
	}
	h.modelsAction(c)
}

func getCompatGroupPlatform(c *gin.Context) string {
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Platform
}
