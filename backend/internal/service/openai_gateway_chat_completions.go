package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

var cursorResponsesUnsupportedFields = []string{
	"prompt_cache_retention",
	"safety_identifier",
	"metadata",
	"stream_options",
}

// ForwardAsChatCompletions accepts a Chat Completions request body, converts it
// to OpenAI Responses API format, forwards to the OpenAI upstream, and converts
// the response back to Chat Completions format.
//
// 历史背景：该函数原本对所有 OpenAI 账号无差别走 CC→Responses 转换 + /v1/responses
// 端点——这在 OAuth（ChatGPT 内部 API 仅支持 Responses）和官方 APIKey 账号上是
// 正确的，但 sub2api 接入 DeepSeek/Kimi/GLM 等第三方 OpenAI 兼容上游后假设破裂：
// 这些上游普遍只支持 /v1/chat/completions，无 /v1/responses 端点。
//
// 当前路由策略（基于账号探测标记，详见 openai_compat.ShouldUseResponsesAPI）：
//   - APIKey 账号 + 探测确认不支持 Responses → 走 forwardAsRawChatCompletions
//     直转上游 /v1/chat/completions，不做协议转换
//   - 其他所有情况（OAuth、APIKey 探测确认支持、未探测）→ 走原有 CC→Responses
//     转换路径（保留旧行为，存量未探测账号零兼容破坏）
func (s *OpenAIGatewayService) ForwardAsChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	promptCacheKey string,
	defaultMappedModel string,
) (*OpenAIForwardResult, error) {
	// 入口分流：APIKey 账号 + 已探测且确认上游不支持 Responses，走 CC 直转。
	// 标记缺失（未探测）按"现状即证据"原则继续走下方原 Responses 转换路径。
	if account.Type == AccountTypeAPIKey && !openai_compat.ShouldUseResponsesAPI(account.Extra) {
		return s.forwardAsRawChatCompletions(ctx, c, account, body, defaultMappedModel)
	}

	startTime := time.Now()

	var chatReq apicompat.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return nil, fmt.Errorf("parse chat completions request: %w", err)
	}
	originalModel := chatReq.Model
	clientStream := chatReq.Stream
	includeUsage := chatReq.StreamOptions != nil && chatReq.StreamOptions.IncludeUsage

	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)

	promptCacheKey = strings.TrimSpace(promptCacheKey)
	compatPromptCacheInjected := false
	if promptCacheKey == "" && account.Type == AccountTypeOAuth && shouldAutoInjectPromptCacheKeyForCompat(upstreamModel) {
		promptCacheKey = deriveCompatPromptCacheKey(&chatReq, upstreamModel)
		compatPromptCacheInjected = promptCacheKey != ""
	}

	isResponsesShape := !gjson.GetBytes(body, "messages").Exists() && gjson.GetBytes(body, "input").Exists()

	var (
		responsesReq  *apicompat.ResponsesRequest
		responsesBody []byte
		err           error
	)
	if isResponsesShape {
		responsesBody, err = sjson.SetBytes(body, "model", upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("rewrite model in responses-shape body: %w", err)
		}
		for _, field := range cursorResponsesUnsupportedFields {
			if stripped, derr := sjson.DeleteBytes(responsesBody, field); derr == nil {
				responsesBody = stripped
			}
		}
		responsesBody, _, err = normalizeOpenAIResponsesFunctionCallOutputStrings(responsesBody)
		if err != nil {
			return nil, fmt.Errorf("normalize function_call_output output in responses-shape body: %w", err)
		}
		responsesBody, _, err = normalizeOpenAIResponsesRequestTools(responsesBody)
		if err != nil {
			return nil, fmt.Errorf("normalize responses tools in responses-shape body: %w", err)
		}
		responsesBody, normalizedServiceTier, err := normalizeResponsesBodyServiceTier(responsesBody)
		if err != nil {
			return nil, fmt.Errorf("normalize service_tier in responses-shape body: %w", err)
		}
		// Minimal stub populated from the raw body so downstream billing
		// propagation (ServiceTier, ReasoningEffort) keeps working.
		responsesReq = &apicompat.ResponsesRequest{
			Model:       upstreamModel,
			ServiceTier: normalizedServiceTier,
		}
		if effort := gjson.GetBytes(responsesBody, "reasoning.effort").String(); effort != "" {
			responsesReq.Reasoning = &apicompat.ResponsesReasoning{Effort: effort}
		}
	} else {
		responsesReq, err = apicompat.ChatCompletionsToResponses(&chatReq)
		if err != nil {
			return nil, fmt.Errorf("convert chat completions to responses: %w", err)
		}
		responsesReq.Model = upstreamModel
		normalizeResponsesRequestServiceTier(responsesReq)
		responsesBody, err = json.Marshal(responsesReq)
		if err != nil {
			return nil, fmt.Errorf("marshal responses request: %w", err)
		}
	}

	logFields := []zap.Field{
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
		zap.Bool("responses_shape", isResponsesShape),
	}
	if compatPromptCacheInjected {
		logFields = append(logFields,
			zap.Bool("compat_prompt_cache_key_injected", true),
			zap.String("compat_prompt_cache_key_sha256", hashSensitiveValueForLog(promptCacheKey)),
		)
	}
	logger.L().Debug("openai chat_completions: model mapping applied", logFields...)
	normalizedModelForTemplate := upstreamModel

	if account.IsOpenAIApiKey() && !account.SupportsOpenAIResponsesUpstream() && account.SupportsOpenAIChatCompletionsUpstream() {
		if c != nil {
			c.Set("openai_upstream_endpoint_override", "/v1/chat/completions")
		}
		directBody := body
		if isResponsesShape {
			directBody, err = normalizeResponsesShapeForChatCompletionsDirect(body)
			if err != nil {
				return nil, err
			}
		}
		return s.forwardOpenAIChatCompletionsDirect(ctx, c, account, directBody, originalModel, billingModel, upstreamModel, promptCacheKey, clientStream, startTime)
	}

	if account.Type == AccountTypeOAuth || promptCacheKey != "" {
		var reqBody map[string]any
		if err := json.Unmarshal(responsesBody, &reqBody); err != nil {
			return nil, fmt.Errorf("unmarshal for responses patch: %w", err)
		}
		if promptCacheKey != "" {
			reqBody["prompt_cache_key"] = promptCacheKey
		}
		if account.Type == AccountTypeOAuth {
			codexResult := applyCodexOAuthTransform(reqBody, false, false)
			if codexResult.NormalizedModel != "" {
				upstreamModel = codexResult.NormalizedModel
				normalizedModelForTemplate = codexResult.NormalizedModel
			}
			if codexResult.PromptCacheKey != "" {
				promptCacheKey = codexResult.PromptCacheKey
			} else if promptCacheKey != "" {
				reqBody["prompt_cache_key"] = promptCacheKey
			}
		}
		forcedTemplateText := ""
		if s.cfg != nil {
			forcedTemplateText = s.cfg.Gateway.ForcedCodexInstructionsTemplate
		}
		if _, err := applyForcedCodexInstructionsTemplateIfNeeded(c, reqBody, forcedTemplateText, forcedCodexInstructionsTemplateData{
			OriginalModel:   originalModel,
			NormalizedModel: normalizedModelForTemplate,
			BillingModel:    billingModel,
			UpstreamModel:   upstreamModel,
		}); err != nil {
			return nil, err
		}
		responsesBody, err = json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("remarshal after responses patch: %w", err)
		}
	}

	// Apply OpenAI fast policy (may filter service_tier or block the request).
	updatedBody, policyErr := s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, responsesBody)
	if policyErr != nil {
		var blocked *OpenAIFastBlockedError
		if errors.As(policyErr, &blocked) {
			writeChatCompletionsError(c, http.StatusForbidden, "permission_error", blocked.Message)
		}
		return nil, policyErr
	}
	responsesBody = updatedBody

	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}

	setOpsUpstreamRequestBody(c, responsesBody)

	// 6. Build upstream request
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	upstreamReq, err := s.buildUpstreamRequest(upstreamCtx, c, account, responsesBody, token, true, promptCacheKey, false)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	if promptCacheKey != "" {
		upstreamReq.Header.Set("session_id", generateSessionUUID(promptCacheKey))
	}

	proxyURL := ""
	if account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(upstreamReq, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: account.Platform, AccountID: account.ID, AccountName: account.Name, UpstreamStatusCode: 0, Kind: "request_error", Message: safeErr})
		writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
		if s.shouldFailoverOpenAIUpstreamResponse(resp.StatusCode, upstreamMsg, respBody) {
			upstreamDetail := ""
			if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
				maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
				if maxBytes <= 0 {
					maxBytes = 2048
				}
				upstreamDetail = truncateString(string(respBody), maxBytes)
			}
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: account.Platform, AccountID: account.ID, AccountName: account.Name, UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get("x-request-id"), Kind: "failover", Message: upstreamMsg, Detail: upstreamDetail})
			if s.rateLimitService != nil {
				s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
			}
			return nil, &UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameAccount: account.IsPoolMode() && (isPoolModeRetryableStatus(resp.StatusCode) || isOpenAITransientProcessingError(resp.StatusCode, upstreamMsg, respBody))}
		}
		return s.handleChatCompletionsErrorResponse(resp, c, account)
	}

	var result *OpenAIForwardResult
	var handleErr error
	if clientStream {
		result, handleErr = s.handleChatStreamingResponse(resp, c, originalModel, billingModel, upstreamModel, includeUsage, startTime)
	} else {
		result, handleErr = s.handleChatBufferedStreamingResponse(resp, c, originalModel, billingModel, upstreamModel, startTime)
	}

	if handleErr == nil && result != nil {
		if responsesReq.ServiceTier != "" {
			st := responsesReq.ServiceTier
			result.ServiceTier = &st
		}
		if responsesReq.Reasoning != nil && responsesReq.Reasoning.Effort != "" {
			re := responsesReq.Reasoning.Effort
			result.ReasoningEffort = &re
		}
	}

	if handleErr == nil && account.Type == AccountTypeOAuth {
		if snapshot := ParseCodexRateLimitHeaders(resp.Header); snapshot != nil {
			s.updateCodexUsageSnapshot(ctx, account.ID, snapshot)
		}
	}

	return result, handleErr
}

func normalizeResponsesRequestServiceTier(req *apicompat.ResponsesRequest) {
	if req == nil {
		return
	}
	req.ServiceTier = normalizedOpenAIServiceTierValue(req.ServiceTier)
}

func normalizeResponsesBodyServiceTier(body []byte) ([]byte, string, error) {
	if len(body) == 0 {
		return body, "", nil
	}
	rawServiceTier := gjson.GetBytes(body, "service_tier").String()
	if rawServiceTier == "" {
		return body, "", nil
	}
	normalizedServiceTier := normalizedOpenAIServiceTierValue(rawServiceTier)
	if normalizedServiceTier == "" {
		trimmed, err := sjson.DeleteBytes(body, "service_tier")
		return trimmed, "", err
	}
	if normalizedServiceTier == rawServiceTier {
		return body, normalizedServiceTier, nil
	}
	trimmed, err := sjson.SetBytes(body, "service_tier", normalizedServiceTier)
	return trimmed, normalizedServiceTier, err
}

func normalizedOpenAIServiceTierValue(raw string) string {
	normalized := normalizeOpenAIServiceTier(raw)
	if normalized == nil {
		return ""
	}
	return *normalized
}

func normalizeResponsesShapeForChatCompletionsDirect(body []byte) ([]byte, error) {
	normalized, err := apicompat.NormalizeResponsesShapeChatCompletionsBody(body)
	if err != nil {
		return nil, fmt.Errorf("normalize responses-shape body for chat completions upstream: %w", err)
	}
	for _, field := range []string{
		"previous_response_id",
		"prompt_cache_retention",
		"safety_identifier",
		"metadata",
		"include",
		"truncation",
		"text",
		"store",
	} {
		if stripped, derr := sjson.DeleteBytes(normalized, field); derr == nil {
			normalized = stripped
		}
	}
	return normalized, nil
}

func (s *OpenAIGatewayService) forwardOpenAIChatCompletionsDirect(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	originalModel string,
	billingModel string,
	upstreamModel string,
	_ string,
	reqStream bool,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	forwardBody := normalizeChatCompletionsBodyForDirectUpstream(body)
	if reqStream {
		forwardBody = ensureChatCompletionsStreamUsage(forwardBody)
	}
	if upstreamModel != "" && upstreamModel != originalModel {
		if patched, err := sjson.SetBytes(forwardBody, "model", upstreamModel); err == nil {
			forwardBody = patched
		}
	}
	forcedTemplateText := ""
	if s.cfg != nil {
		forcedTemplateText = s.cfg.Gateway.ForcedCodexInstructionsTemplate
	}
	if patched, _, err := applyForcedCodexInstructionsTemplateToChatCompletionsBodyIfNeeded(c, forwardBody, forcedTemplateText, forcedCodexInstructionsTemplateData{
		OriginalModel:   originalModel,
		NormalizedModel: upstreamModel,
		BillingModel:    billingModel,
		UpstreamModel:   upstreamModel,
	}); err != nil {
		return nil, err
	} else {
		forwardBody = patched
	}
	if c != nil && upstreamModel != "" && upstreamModel != originalModel {
		c.Set("openai_passthrough_model_rewrite_from", upstreamModel)
		c.Set("openai_passthrough_model_rewrite_to", originalModel)
	}

	reasoningEffort := extractOpenAIReasoningEffortFromBody(body, originalModel)
	result, err := s.forwardOpenAIPassthrough(ctx, c, account, forwardBody, originalModel, reasoningEffort, reqStream, startTime)
	if result != nil {
		result.BillingModel = billingModel
		result.UpstreamModel = upstreamModel
	}
	return result, err
}

func normalizeChatCompletionsBodyForDirectUpstream(body []byte) []byte {
	if len(body) == 0 || !gjson.GetBytes(body, "system").Exists() || !gjson.GetBytes(body, "messages").IsArray() {
		return body
	}
	systemText := extractAnthropicSystemTextFromBody(body)
	if strings.TrimSpace(systemText) == "" {
		if stripped, err := sjson.DeleteBytes(body, "system"); err == nil {
			return stripped
		}
		return body
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	rawMessages, ok := payload["messages"].([]any)
	if !ok {
		return body
	}
	payload["messages"] = append([]any{map[string]any{"role": "system", "content": systemText}}, rawMessages...)
	delete(payload, "system")

	normalized, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return normalized
}

func extractAnthropicSystemTextFromBody(body []byte) string {
	raw := gjson.GetBytes(body, "system")
	if !raw.Exists() {
		return ""
	}
	var value any
	if err := json.Unmarshal([]byte(raw.Raw), &value); err != nil {
		return ""
	}
	return strings.TrimSpace(extractAnthropicSystemText(value))
}

func ensureChatCompletionsStreamUsage(body []byte) []byte {
	if len(body) == 0 || gjson.GetBytes(body, "stream_options.include_usage").Bool() {
		return body
	}
	patched, err := sjson.SetBytes(body, "stream_options.include_usage", true)
	if err != nil {
		return body
	}
	return patched
}

func (s *OpenAIGatewayService) handleChatCompletionsErrorResponse(resp *http.Response, c *gin.Context, account *Account) (*OpenAIForwardResult, error) {
	return s.handleCompatErrorResponse(resp, c, account, writeChatCompletionsError)
}

func (s *OpenAIGatewayService) handleChatBufferedStreamingResponse(resp *http.Response, c *gin.Context, originalModel string, billingModel string, upstreamModel string, startTime time.Time) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	finalResponse, usage, acc, err := s.readOpenAICompatBufferedTerminal(resp, "openai chat_completions buffered", requestID)
	if err != nil {
		return nil, err
	}

	if finalResponse == nil {
		writeChatCompletionsError(c, http.StatusBadGateway, "api_error", "Upstream stream ended without a terminal response event")
		return nil, fmt.Errorf("upstream stream ended without terminal event")
	}

	acc.SupplementResponseOutput(finalResponse)
	chatResp := apicompat.ResponsesToChatCompletions(finalResponse, originalModel)
	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.JSON(http.StatusOK, chatResp)

	return &OpenAIForwardResult{RequestID: requestID, Usage: usage, Model: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel, Stream: false, Duration: time.Since(startTime)}, nil
}

func (s *OpenAIGatewayService) handleChatStreamingResponse(resp *http.Response, c *gin.Context, originalModel string, billingModel string, upstreamModel string, includeUsage bool, startTime time.Time) (*OpenAIForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	state := apicompat.NewResponsesEventToChatState()
	state.Model = originalModel
	state.IncludeUsage = includeUsage

	var usage OpenAIUsage
	var firstTokenMs *int
	firstChunk := true
	clientDisconnected := false

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	streamInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
	}

	resultWithUsage := func() *OpenAIForwardResult {
		return &OpenAIForwardResult{RequestID: requestID, Usage: usage, Model: originalModel, BillingModel: billingModel, UpstreamModel: upstreamModel, Stream: true, Duration: time.Since(startTime), FirstTokenMs: firstTokenMs}
	}

	processDataLine := func(payload string) bool {
		if firstChunk {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}
		var event apicompat.ResponsesStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			logger.L().Warn("openai chat_completions stream: failed to parse event", zap.Error(err), zap.String("request_id", requestID))
			return false
		}

		// 仅按兼容转换器支持的终止事件提取 usage，避免无意扩大事件语义。
		isTerminalEvent := isOpenAICompatResponsesTerminalEvent(event.Type)
		if isTerminalEvent && event.Response != nil && event.Response.Usage != nil {
			usage = copyOpenAIUsageFromResponsesUsage(event.Response.Usage)
		}
		chunks := apicompat.ResponsesEventToChatChunks(&event, state)
		if !clientDisconnected {
			for _, chunk := range chunks {
				sse, err := apicompat.ChatChunkToSSE(chunk)
				if err != nil {
					logger.L().Warn("openai chat_completions stream: failed to marshal chunk",
						zap.Error(err),
						zap.String("request_id", requestID),
					)
					continue
				}
				if _, err := fmt.Fprint(c.Writer, sse); err != nil {
					clientDisconnected = true
					logger.L().Info("openai chat_completions stream: client disconnected, continuing to drain upstream for billing",
						zap.String("request_id", requestID),
					)
					break
				}
			}
		}
		if len(chunks) > 0 && !clientDisconnected {
			c.Writer.Flush()
		}
		return isTerminalEvent
	}

	finalizeStream := func() (*OpenAIForwardResult, error) {
		if finalChunks := apicompat.FinalizeResponsesChatStream(state); len(finalChunks) > 0 && !clientDisconnected {
			for _, chunk := range finalChunks {
				sse, err := apicompat.ChatChunkToSSE(chunk)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprint(c.Writer, sse); err != nil {
					clientDisconnected = true
					logger.L().Info("openai chat_completions stream: client disconnected during final flush",
						zap.String("request_id", requestID),
					)
					break
				}
			}
		}
		// Send [DONE] sentinel
		if !clientDisconnected {
			if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err != nil {
				clientDisconnected = true
				logger.L().Info("openai chat_completions stream: client disconnected during done flush",
					zap.String("request_id", requestID),
				)
			}
		}
		if !clientDisconnected {
			c.Writer.Flush()
		}
		return resultWithUsage(), nil
	}

	handleScanErr := func(err error) {
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("openai chat_completions stream: read error", zap.Error(err), zap.String("request_id", requestID))
		}
	}
	missingTerminalErr := func() (*OpenAIForwardResult, error) {
		return resultWithUsage(), fmt.Errorf("stream usage incomplete: missing terminal event")
	}

	keepaliveInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamKeepaliveInterval > 0 {
		keepaliveInterval = time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
	}

	// No keepalive: fast synchronous path
	if streamInterval <= 0 && keepaliveInterval <= 0 {
		for scanner.Scan() {
			line := scanner.Text()
			payload, ok := extractOpenAISSEDataLine(line)
			if !ok {
				continue
			}
			if strings.TrimSpace(payload) == "[DONE]" {
				return missingTerminalErr()
			}
			if processDataLine(payload) {
				return finalizeStream()
			}
		}
		if err := scanner.Err(); err != nil {
			handleScanErr(err)
			return resultWithUsage(), fmt.Errorf("stream usage incomplete: %w", err)
		}
		return missingTerminalErr()
	}

	type scanEvent struct {
		line string
		err  error
	}
	events := make(chan scanEvent, 16)
	done := make(chan struct{})
	var lastReadAt int64
	atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
	sendEvent := func(ev scanEvent) bool {
		select {
		case events <- ev:
			return true
		case <-done:
			return false
		}
	}
	go func() {
		defer close(events)
		for scanner.Scan() {
			atomic.StoreInt64(&lastReadAt, time.Now().UnixNano())
			if !sendEvent(scanEvent{line: scanner.Text()}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			_ = sendEvent(scanEvent{err: err})
		}
	}()
	defer close(done)

	var keepaliveTicker *time.Ticker
	if keepaliveInterval > 0 {
		keepaliveTicker = time.NewTicker(keepaliveInterval)
		defer keepaliveTicker.Stop()
	}
	var keepaliveCh <-chan time.Time
	if keepaliveTicker != nil {
		keepaliveCh = keepaliveTicker.C
	}
	lastDataAt := time.Now()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return missingTerminalErr()
			}
			if ev.err != nil {
				handleScanErr(ev.err)
				return resultWithUsage(), fmt.Errorf("stream usage incomplete: %w", ev.err)
			}
			lastDataAt = time.Now()
			line := ev.line
			payload, ok := extractOpenAISSEDataLine(line)
			if !ok {
				continue
			}
			if strings.TrimSpace(payload) == "[DONE]" {
				return missingTerminalErr()
			}
			if processDataLine(payload) {
				return finalizeStream()
			}

		case <-intervalCh:
			lastRead := time.Unix(0, atomic.LoadInt64(&lastReadAt))
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if clientDisconnected {
				return resultWithUsage(), fmt.Errorf("stream usage incomplete after timeout")
			}
			logger.L().Warn("openai chat_completions stream: data interval timeout",
				zap.String("request_id", requestID),
				zap.String("model", originalModel),
				zap.Duration("interval", streamInterval),
			)
			return resultWithUsage(), fmt.Errorf("stream data interval timeout")

		case <-keepaliveCh:
			if clientDisconnected {
				continue
			}
			if time.Since(lastDataAt) < keepaliveInterval {
				continue
			}
			if _, err := fmt.Fprint(c.Writer, ":\n\n"); err != nil {
				logger.L().Info("openai chat_completions stream: client disconnected during keepalive",
					zap.String("request_id", requestID),
				)
				clientDisconnected = true
				continue
			}
			c.Writer.Flush()
		}
	}
}

func writeChatCompletionsError(c *gin.Context, statusCode int, errType, message string) {
	c.JSON(statusCode, gin.H{"error": gin.H{"type": errType, "message": message}})
}
