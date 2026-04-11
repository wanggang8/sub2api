package service

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func isCursorOpenAICompatPath(path string) bool {
	normalized := strings.TrimRight(strings.TrimSpace(path), "/")
	return normalized == "/cursor/v1/responses" || normalized == "/cursor/v1/chat/completions"
}

// ResolveCursorCompatPromptCacheKey returns an existing session marker or,
// when enabled for this account and request path, derives a deterministic
// prompt_cache_key from the request content for Cursor OpenAI compat requests.
func (s *OpenAIGatewayService) ResolveCursorCompatPromptCacheKey(c *gin.Context, body []byte) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if !isCursorOpenAICompatPath(c.Request.URL.Path) {
		return ""
	}
	if existing := s.ExtractSessionID(c, body); existing != "" {
		return existing
	}
	seed := deriveOpenAIContentSessionSeed(body)
	if seed == "" {
		return ""
	}
	return GenerateSessionUUID(seed)
}

// InjectOpenAIPromptCacheKeyIfMissing patches prompt_cache_key into the body
// only when it is absent and a non-empty key was resolved.
func InjectOpenAIPromptCacheKeyIfMissing(body []byte, promptCacheKey string) []byte {
	if len(body) == 0 || strings.TrimSpace(promptCacheKey) == "" {
		return body
	}
	if existing := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); existing != "" {
		return body
	}
	patched, err := sjson.SetBytes(body, "prompt_cache_key", promptCacheKey)
	if err != nil {
		return body
	}
	return patched
}
