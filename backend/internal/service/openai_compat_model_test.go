package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAICompatRequestedModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "gpt reasoning alias strips xhigh", input: "gpt-5.4-xhigh", want: "gpt-5.4"},
		{name: "gpt reasoning alias strips none", input: "gpt-5.4-none", want: "gpt-5.4"},
		{name: "codex max model stays intact", input: "gpt-5.1-codex-max", want: "gpt-5.1-codex-max"},
		{name: "non openai model unchanged", input: "claude-opus-4-6", want: "claude-opus-4-6"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, NormalizeOpenAICompatRequestedModel(tt.input))
		})
	}
}

func TestApplyOpenAICompatModelNormalization(t *testing.T) {
	t.Parallel()

	t.Run("derives xhigh from model suffix when output config missing", func(t *testing.T) {
		req := &apicompat.AnthropicRequest{Model: "gpt-5.4-xhigh"}

		applyOpenAICompatModelNormalization(req)

		require.Equal(t, "gpt-5.4", req.Model)
		require.NotNil(t, req.OutputConfig)
		require.Equal(t, "max", req.OutputConfig.Effort)
	})

	t.Run("explicit output config wins over model suffix", func(t *testing.T) {
		req := &apicompat.AnthropicRequest{
			Model:        "gpt-5.4-xhigh",
			OutputConfig: &apicompat.AnthropicOutputConfig{Effort: "low"},
		}

		applyOpenAICompatModelNormalization(req)

		require.Equal(t, "gpt-5.4", req.Model)
		require.NotNil(t, req.OutputConfig)
		require.Equal(t, "low", req.OutputConfig.Effort)
	})

	t.Run("non openai model is untouched", func(t *testing.T) {
		req := &apicompat.AnthropicRequest{Model: "claude-opus-4-6"}

		applyOpenAICompatModelNormalization(req)

		require.Equal(t, "claude-opus-4-6", req.Model)
		require.Nil(t, req.OutputConfig)
	})
}

func TestForwardAsAnthropic_NormalizesRoutingAndEffortForGpt54XHigh(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4-xhigh","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_compat"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
			"model_mapping": map[string]any{
				"gpt-5.4": "gpt-5.4",
			},
		},
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "gpt-5.1")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.4-xhigh", result.Model)
	require.Equal(t, "gpt-5.4", result.UpstreamModel)
	require.Equal(t, "gpt-5.4", result.BillingModel)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "xhigh", *result.ReasoningEffort)

	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "xhigh", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "gpt-5.4-xhigh", gjson.GetBytes(rec.Body.Bytes(), "model").String())
	require.Equal(t, "ok", gjson.GetBytes(rec.Body.Bytes(), "content.0.text").String())
	t.Logf("upstream body: %s", string(upstream.lastBody))
	t.Logf("response body: %s", rec.Body.String())
}

func TestForwardAsAnthropic_ForcedCodexInstructionsTemplatePrependsRenderedInstructions(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	templateDir := t.TempDir()
	templatePath := filepath.Join(templateDir, "codex-instructions.md.tmpl")
	require.NoError(t, os.WriteFile(templatePath, []byte("server-prefix\n\n{{ .ExistingInstructions }}"), 0o644))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","max_tokens":16,"system":"client-system","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_forced"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			ForcedCodexInstructionsTemplateFile: templatePath,
			ForcedCodexInstructionsTemplate:     "server-prefix\n\n{{ .ExistingInstructions }}",
		}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "gpt-5.1")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "server-prefix\n\nclient-system", gjson.GetBytes(upstream.lastBody, "instructions").String())
}

func TestForwardAsAnthropic_ForcedCodexInstructionsTemplateUsesCachedTemplateContent(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","max_tokens":16,"system":"client-system","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_forced_cached"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			ForcedCodexInstructionsTemplateFile: "/path/that/should/not/be/read.tmpl",
			ForcedCodexInstructionsTemplate:     "cached-prefix\n\n{{ .ExistingInstructions }}",
		}},
		httpUpstream: upstream,
	}
	account := &Account{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "gpt-5.1")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "cached-prefix\n\nclient-system", gjson.GetBytes(upstream.lastBody, "instructions").String())
}

func TestForwardAsAnthropic_DirectMessagesParsesStreamingUsage(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"gpt-5.4","content":[],"usage":{"input_tokens":21,"cache_read_input_tokens":6}}}`,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":8}}`,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_messages_stream_usage"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": false,
			"openai_upstream_supports_messages":         true,
		},
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/messages", upstream.lastReq.URL.Path)
	require.Equal(t, 21, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, 6, result.Usage.CacheReadInputTokens)
}

func TestForwardAsAnthropic_DirectMessagesEstimatesUsageWhenMissing(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","max_tokens":16,"system":"You are concise.","messages":[{"role":"user","content":"hello world"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"estimated answer"}}`,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_messages_stream_estimated_usage"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": false,
			"openai_upstream_supports_messages":         true,
		},
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, result.Usage.InputTokens, 0)
	require.Greater(t, result.Usage.OutputTokens, 0)
}

func TestForwardAsAnthropic_DirectMessagesEstimatesMissingOutputWhenInputUsagePresent(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","max_tokens":16,"messages":[{"role":"user","content":"hello world"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"gpt-5.4","content":[],"usage":{"input_tokens":19}}}`,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"estimated output"}}`,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_messages_partial_usage"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": false,
			"openai_upstream_supports_messages":         true,
		},
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 19, result.Usage.InputTokens)
	require.Greater(t, result.Usage.OutputTokens, 0)
}

func TestForwardAsChatCompletions_DirectsToUpstreamChatCompletionsWhenResponsesUnsupported(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chat_direct"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl_1",
			"object":"chat.completion",
			"model":"gpt-5.4",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}
		}`)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": true,
			"openai_upstream_supports_messages":         false,
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/chat/completions", upstream.lastReq.URL.Path)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").Exists())
}

func TestForwardAsChatCompletions_DirectChatNormalizesTopLevelSystem(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{
		"model":"gpt-5.4",
		"system":[{"type":"text","text":"Use tools as tool calls."}],
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{"type":"function","function":{"name":"Read","parameters":{"type":"object"}}}],
		"stream":false
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chat_direct_system"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl_1",
			"object":"chat.completion",
			"model":"gpt-5.4",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}
		}`)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": true,
			"openai_upstream_supports_messages":         false,
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/chat/completions", upstream.lastReq.URL.Path)
	require.False(t, gjson.GetBytes(upstream.lastBody, "system").Exists())
	require.Equal(t, "system", gjson.GetBytes(upstream.lastBody, "messages.0.role").String())
	require.Equal(t, "Use tools as tool calls.", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.Equal(t, "user", gjson.GetBytes(upstream.lastBody, "messages.1.role").String())
	require.Len(t, gjson.GetBytes(upstream.lastBody, "tools").Array(), 1)
}

func TestForwardAsChatCompletions_DirectChatConvertsResponsesShape(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{
		"model":"gpt-5.4",
		"instructions":"system prompt",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"tools":[
			{"name":"Read","description":"Read file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}},
			{"type":"function","function":{"name":"Grep","parameters":{"type":"object","properties":{"pattern":{"type":"string"}}}}},
			{"type":"web_search"}
		],
		"tool_choice":{"type":"any"},
		"max_output_tokens":64,
		"reasoning":{"effort":"high"},
		"previous_response_id":"resp_ignored",
		"prompt_cache_retention":"24h",
		"include":["reasoning.encrypted_content"],
		"truncation":"auto",
		"text":{"format":{"type":"text"}},
		"stream":false
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chat_direct_responses_shape"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl_1",
			"object":"chat.completion",
			"model":"gpt-5.4",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}
		}`)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": true,
			"openai_upstream_supports_messages":         false,
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/chat/completions", upstream.lastReq.URL.Path)
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_output_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "reasoning").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_retention").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "include").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "truncation").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "text").Exists())
	require.Equal(t, "system", gjson.GetBytes(upstream.lastBody, "messages.0.role").String())
	require.Equal(t, "system prompt", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.Equal(t, "user", gjson.GetBytes(upstream.lastBody, "messages.1.role").String())
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "messages.1.content").String())
	require.Equal(t, int64(64), gjson.GetBytes(upstream.lastBody, "max_completion_tokens").Int())
	require.Equal(t, "high", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
	require.Equal(t, "required", gjson.GetBytes(upstream.lastBody, "tool_choice").String())

	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "Read", tools[0].Get("function.name").String())
	require.Equal(t, "string", tools[0].Get("function.parameters.properties.path.type").String())
	require.Equal(t, "function", tools[1].Get("type").String())
	require.Equal(t, "Grep", tools[1].Get("function.name").String())
	require.Equal(t, "web_search", tools[2].Get("type").String())
}

func TestForwardAsChatCompletionsResponsesShapeNormalizesFunctionCallOutputArray(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{
		"model":"gpt-5.4",
		"input":[{
			"type":"function_call_output",
			"call_id":"call_1",
			"output":[
				{"type":"text","text":"line one"},
				{"type":"output_text","text":"line two"}
			]
		}],
		"stream":false
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_responses_shape_tool_array"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        true,
			"openai_upstream_supports_chat_completions": false,
			"openai_upstream_supports_messages":         false,
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/responses", upstream.lastReq.URL.Path)
	require.Equal(t, "line one\nline two", gjson.GetBytes(upstream.lastBody, "input.0.output").String())
	require.Equal(t, gjson.String, gjson.GetBytes(upstream.lastBody, "input.0.output").Type)
}

func TestForwardAsChatCompletionsResponsesShapeNormalizesTools(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{
		"model":"gpt-5.4",
		"input":[{"role":"user","content":"hello"}],
		"tools":[
			{"name":"Read","description":"Read file","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}},
			{"type":"function","function":{"name":"Grep","description":"Search files","parameters":{"type":"object","properties":{"pattern":{"type":"string"}}},"strict":true}},
			{"type":"web_search"}
		],
		"tool_choice":{"type":"any"},
		"stream":false
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_responses_shape_tools"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        true,
			"openai_upstream_supports_chat_completions": false,
			"openai_upstream_supports_messages":         false,
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/responses", upstream.lastReq.URL.Path)

	tools := gjson.GetBytes(upstream.lastBody, "tools").Array()
	require.Len(t, tools, 3)
	require.Equal(t, "function", tools[0].Get("type").String())
	require.Equal(t, "Read", tools[0].Get("name").String())
	require.Equal(t, "Read file", tools[0].Get("description").String())
	require.Equal(t, "string", tools[0].Get("parameters.properties.path.type").String())
	require.False(t, tools[0].Get("input_schema").Exists())
	require.False(t, tools[0].Get("function").Exists())

	require.Equal(t, "function", tools[1].Get("type").String())
	require.Equal(t, "Grep", tools[1].Get("name").String())
	require.Equal(t, "Search files", tools[1].Get("description").String())
	require.Equal(t, "string", tools[1].Get("parameters.properties.pattern.type").String())
	require.True(t, tools[1].Get("strict").Bool())
	require.False(t, tools[1].Get("function").Exists())

	require.Equal(t, "web_search", tools[2].Get("type").String())
	require.Equal(t, "required", gjson.GetBytes(upstream.lastBody, "tool_choice").String())
}

func TestForwardAsChatCompletions_DirectStreamRequestsUsageAndParsesChatUsage(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":13,"completion_tokens":4,"total_tokens":17,"prompt_tokens_details":{"cached_tokens":2}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_stream_usage"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": true,
			"openai_upstream_supports_messages":         false,
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/chat/completions", upstream.lastReq.URL.Path)
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
}

func TestForwardAsChatCompletions_DirectStreamEstimatesUsageWhenMissing(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello world"}],"stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"estimated answer"},"finish_reason":null}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_chat_stream_estimated_usage"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": true,
			"openai_upstream_supports_messages":         false,
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, result.Usage.InputTokens, 0)
	require.Greater(t, result.Usage.OutputTokens, 0)
}

func TestForwardAsChatCompletions_DirectNonStreamEstimatesOutputWhenUsageMissing(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello world"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chat_nonstream_estimated_usage"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl_1",
			"object":"chat.completion",
			"model":"gpt-5.4",
			"choices":[{"index":0,"message":{"role":"assistant","content":"estimated answer"},"finish_reason":"stop"}]
		}`)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": true,
			"openai_upstream_supports_messages":         false,
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Greater(t, result.Usage.InputTokens, 0)
	require.Greater(t, result.Usage.OutputTokens, 0)
}

func TestForwardAsChatCompletions_DirectChatPathStillAppliesForcedTemplateAndPreservesClientModel(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"CustomModel","messages":[{"role":"system","content":"client-system"},{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	SetForcedCodexInstructionsEnabled(c, true)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_chat_direct_template"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"chatcmpl_1",
			"object":"chat.completion",
			"model":"gpt-5.4",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}
		}`)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			ForcedCodexInstructionsTemplate: "server-prefix\n\n{{ .ExistingInstructions }}",
		}},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
			"model_mapping": map[string]any{
				"CustomModel": "gpt-5.4",
			},
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": true,
			"openai_upstream_supports_messages":         false,
		},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/chat/completions", upstream.lastReq.URL.Path)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "server-prefix\n\nclient-system", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.Equal(t, "CustomModel", gjson.GetBytes(rec.Body.Bytes(), "model").String())
}

func TestBuildUpstreamRequestOpenAIPassthrough_DirectsToUpstreamMessagesWhenOverridden(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{"model":"gpt-5"}`)))
	c.Set("openai_upstream_endpoint_override", "/v1/messages")

	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "k-msg",
			"base_url": "https://gateway.example/v1",
		},
	}

	req, err := svc.buildUpstreamRequestOpenAIPassthrough(c.Request.Context(), c, account, []byte(`{"model":"gpt-5"}`), "token")
	require.NoError(t, err)
	require.Equal(t, "https://gateway.example/v1/messages", req.URL.String())
}

func TestForwardAsAnthropic_DirectsToUpstreamMessagesWhenResponsesUnsupported(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-opus-4-1","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_msg_direct"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"gpt-5",
			"content":[{"type":"text","text":"ok"}],
			"usage":{"input_tokens":5,"output_tokens":2}
		}`)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey-msg",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": false,
			"openai_upstream_supports_messages":         true,
		},
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/messages", upstream.lastReq.URL.Path)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "message", gjson.GetBytes(rec.Body.Bytes(), "type").String())
	require.Equal(t, "ok", gjson.GetBytes(rec.Body.Bytes(), "content.0.text").String())
}

func TestForwardAsAnthropic_DirectMessagesPathStillAppliesForcedTemplateAndPreservesClientModel(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"claude-opus-4-1","system":"client-system","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	SetForcedCodexInstructionsEnabled(c, true)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_msg_direct_template"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"gpt-5",
			"content":[{"type":"text","text":"ok"}],
			"usage":{"input_tokens":5,"output_tokens":2}
		}`)),
	}}

	svc := &OpenAIGatewayService{
		httpUpstream: upstream,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			ForcedCodexInstructionsTemplate: "server-prefix\n\n{{ .ExistingInstructions }}",
		}},
	}
	account := &Account{
		ID:          1,
		Name:        "openai-apikey-msg",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "api-key",
			"base_url": "https://relay.example.com",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": false,
			"openai_upstream_supports_messages":         true,
		},
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "/v1/messages", upstream.lastReq.URL.Path)
	require.Equal(t, "server-prefix\n\nclient-system", gjson.GetBytes(upstream.lastBody, "system").String())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "claude-opus-4-1", gjson.GetBytes(rec.Body.Bytes(), "model").String())
	require.Equal(t, "ok", gjson.GetBytes(rec.Body.Bytes(), "content.0.text").String())
}
