package admin

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"

	"github.com/gin-gonic/gin"
)

type upstreamModelsPreviewRequest struct {
	Platform      string `json:"platform" binding:"required"`
	BaseURL       string `json:"base_url" binding:"required"`
	APIKey        string `json:"api_key" binding:"required"`
	SkipTLSVerify bool   `json:"skip_tls_verify"`
}

type upstreamModelListEnvelope[T any] struct {
	Data []T `json:"data"`
}

type upstreamOpenAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type upstreamGeminiModel struct {
	Name         string   `json:"name"`
	DisplayName  string   `json:"displayName"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	InputToken   int      `json:"inputTokenLimit"`
	OutputToken  int      `json:"outputTokenLimit"`
	SupportedOps []string `json:"supportedGenerationMethods"`
}

const upstreamModelsPreviewTimeout = 10 * time.Second

func defaultUpstreamModelsResult(platform, message string) gin.H {
	payload := gin.H{
		"models":  defaultPreviewModelsForPlatform(platform),
		"source":  "fallback",
		"message": message,
	}
	if strings.TrimSpace(message) == "" {
		delete(payload, "message")
	}
	return payload
}

func defaultPreviewModelsForPlatform(platform string) any {
	switch strings.TrimSpace(strings.ToLower(platform)) {
	case service.PlatformOpenAI:
		return openai.DefaultModels
	case service.PlatformGemini:
		return geminicli.DefaultModels
	default:
		return claude.DefaultModels
	}
}

func normalizeOpenAIUpstreamModels(models []upstreamOpenAIModel) []openai.Model {
	if len(models) == 0 {
		return nil
	}
	result := make([]openai.Model, 0, len(models))
	for _, item := range models {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		result = append(result, openai.Model{
			ID:          id,
			Object:      "model",
			Created:     item.Created,
			OwnedBy:     item.OwnedBy,
			Type:        "model",
			DisplayName: id,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

func normalizeGeminiUpstreamModels(models []upstreamGeminiModel) []geminicli.Model {
	if len(models) == 0 {
		return nil
	}
	result := make([]geminicli.Model, 0, len(models))
	for _, item := range models {
		id := strings.TrimSpace(strings.TrimPrefix(item.Name, "models/"))
		if id == "" {
			continue
		}
		displayName := strings.TrimSpace(item.DisplayName)
		if displayName == "" {
			displayName = id
		}
		result = append(result, geminicli.Model{
			ID:          id,
			Type:        "model",
			DisplayName: displayName,
			CreatedAt:   "",
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

func buildOpenAIModelListURL(baseURL string) string {
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(normalized, "/models") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/v1") {
		return normalized + "/models"
	}
	return normalized + "/v1/models"
}

func buildAnthropicModelListURL(baseURL string) string {
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(normalized, "/models") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/v1") {
		return normalized + "/models"
	}
	return normalized + "/v1/models"
}

func buildGeminiModelListURL(baseURL string) string {
	normalized := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.Contains(normalized, "/v1beta/models") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/v1beta") {
		return normalized + "/models"
	}
	if strings.HasSuffix(normalized, "/models") {
		return normalized
	}
	return normalized + "/v1beta/models"
}

func newUpstreamPreviewHTTPClient(skipTLSVerify bool) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if skipTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &http.Client{Timeout: upstreamModelsPreviewTimeout, Transport: transport}
}

func fetchPreviewResponse(ctx context.Context, client *http.Client, requestURL string, headers map[string]string) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, upstreamModelsPreviewTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		if strings.TrimSpace(v) != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyText := strings.TrimSpace(string(body))
		if bodyText == "" {
			return nil, fmt.Errorf("upstream returned status %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream returned status %d: %s", resp.StatusCode, bodyText)
	}
	return body, nil
}

func (h *AccountHandler) fetchUpstreamModels(ctx context.Context, req upstreamModelsPreviewRequest) (any, error) {
	platform := strings.TrimSpace(strings.ToLower(req.Platform))
	trimmedBaseURL := strings.TrimSpace(req.BaseURL)
	trimmedAPIKey := strings.TrimSpace(req.APIKey)
	if trimmedBaseURL == "" || trimmedAPIKey == "" || platform == "" {
		return nil, errors.New("platform, base_url and api_key are required")
	}
	normalizedBaseURL, err := urlvalidator.ValidateURLFormat(trimmedBaseURL, true)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}
	client := newUpstreamPreviewHTTPClient(req.SkipTLSVerify)

	switch platform {
	case service.PlatformOpenAI:
		body, err := fetchPreviewResponse(ctx, client, buildOpenAIModelListURL(normalizedBaseURL), map[string]string{
			"Authorization": "Bearer " + trimmedAPIKey,
			"Accept":        "application/json",
		})
		if err != nil {
			return nil, err
		}
		var envelope upstreamModelListEnvelope[upstreamOpenAIModel]
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, err
		}
		models := normalizeOpenAIUpstreamModels(envelope.Data)
		if len(models) == 0 {
			return nil, errors.New("upstream returned empty model list")
		}
		return models, nil
	case service.PlatformGemini:
		body, err := fetchPreviewResponse(ctx, client, buildGeminiModelListURL(normalizedBaseURL), map[string]string{
			"x-goog-api-key": trimmedAPIKey,
			"Accept":         "application/json",
		})
		if err != nil {
			return nil, err
		}
		var envelope upstreamModelListEnvelope[upstreamGeminiModel]
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, err
		}
		models := normalizeGeminiUpstreamModels(envelope.Data)
		if len(models) == 0 {
			return nil, errors.New("upstream returned empty model list")
		}
		return models, nil
	case service.PlatformAnthropic:
		body, err := fetchPreviewResponse(ctx, client, buildAnthropicModelListURL(normalizedBaseURL), map[string]string{
			"x-api-key":         trimmedAPIKey,
			"anthropic-version": "2023-06-01",
			"Accept":            "application/json",
		})
		if err != nil {
			return nil, err
		}
		var envelope upstreamModelListEnvelope[upstreamOpenAIModel]
		if err := json.Unmarshal(body, &envelope); err != nil {
			return nil, err
		}
		models := normalizeOpenAIUpstreamModels(envelope.Data)
		if len(models) == 0 {
			return nil, errors.New("upstream returned empty model list")
		}
		return models, nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", platform)
	}
}
