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

func ResolveCursorCompatPromptCacheKey(c *gin.Context, body []byte) string {
	if c == nil || c.Request == nil || !isCursorOpenAICompatPath(c.Request.URL.Path) {
		return ""
	}
	if existing := strings.TrimSpace(c.GetHeader("session_id")); existing != "" {
		return existing
	}
	if existing := strings.TrimSpace(c.GetHeader("conversation_id")); existing != "" {
		return existing
	}
	if existing := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); existing != "" {
		return existing
	}
	if uid := ParseMetadataUserID(strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String())); uid != nil && uid.SessionID != "" {
		return uid.SessionID
	}
	seed := deriveOpenAIContentSessionSeed(body)
	if seed == "" {
		return ""
	}
	return GenerateSessionUUID(seed)
}

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
