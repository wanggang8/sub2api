package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const anthropicContentSessionSeedPrefix = "compat_as_"

func isCursorAnthropicCompatPath(path string) bool {
	normalized := strings.TrimRight(strings.TrimSpace(path), "/")
	switch normalized {
	case "/cursor/v1/messages", "/cursor/v1/chat/completions", "/cursor/v1/responses":
		return true
	default:
		return false
	}
}

func deriveAnthropicCursorContentSessionSeed(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var b strings.Builder

	if model := gjson.GetBytes(body, "model").String(); model != "" {
		_, _ = b.WriteString("model=")
		_, _ = b.WriteString(model)
	}

	if tools := gjson.GetBytes(body, "tools"); tools.Exists() && tools.IsArray() && tools.Raw != "[]" {
		_, _ = b.WriteString("|tools=")
		_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(tools.Raw)))
	}

	if system := gjson.GetBytes(body, "system"); system.Exists() {
		_, _ = b.WriteString("|system=")
		_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(system.Raw)))
	}

	if instructions := gjson.GetBytes(body, "instructions").String(); instructions != "" {
		_, _ = b.WriteString("|instructions=")
		_, _ = b.WriteString(instructions)
	}

	firstUserCaptured := false
	if messages := gjson.GetBytes(body, "messages"); messages.Exists() && messages.IsArray() {
		messages.ForEach(func(_, msg gjson.Result) bool {
			role := strings.TrimSpace(msg.Get("role").String())
			switch role {
			case "system", "developer":
				if system := msg.Get("content"); system.Exists() {
					_, _ = b.WriteString("|system=")
					_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(system.Raw)))
				}
			case "user":
				if !firstUserCaptured {
					if content := msg.Get("content"); content.Exists() {
						_, _ = b.WriteString("|first_user=")
						_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(content.Raw)))
						firstUserCaptured = true
					}
				}
			}
			return true
		})
	}

	if !firstUserCaptured {
		if input := gjson.GetBytes(body, "input"); input.Exists() {
			if input.Type == gjson.String {
				_, _ = b.WriteString("|first_user=")
				_, _ = b.WriteString(input.String())
				firstUserCaptured = true
			} else if input.IsArray() {
				input.ForEach(func(_, item gjson.Result) bool {
					role := strings.TrimSpace(item.Get("role").String())
					if role == "system" || role == "developer" {
						if content := item.Get("content"); content.Exists() {
							_, _ = b.WriteString("|system=")
							_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(content.Raw)))
						}
						return true
					}
					if role == "user" && !firstUserCaptured {
						if content := item.Get("content"); content.Exists() {
							_, _ = b.WriteString("|first_user=")
							_, _ = b.WriteString(normalizeCompatSeedJSON(json.RawMessage(content.Raw)))
							firstUserCaptured = true
						}
					}
					if !firstUserCaptured && item.Get("type").String() == "input_text" {
						if text := item.Get("text").String(); text != "" {
							_, _ = b.WriteString("|first_user=")
							_, _ = b.WriteString(text)
							firstUserCaptured = true
						}
					}
					return true
				})
			}
		}
	}

	if b.Len() == 0 {
		return ""
	}
	return anthropicContentSessionSeedPrefix + b.String()
}

func ResolveCursorCompatAnthropicSessionID(c *gin.Context, body []byte) string {
	if c == nil || c.Request == nil || !isCursorAnthropicCompatPath(c.Request.URL.Path) {
		return ""
	}

	if existing := strings.TrimSpace(c.GetHeader("X-Claude-Code-Session-Id")); existing != "" {
		return existing
	}
	if existing := strings.TrimSpace(c.GetHeader("session_id")); existing != "" {
		return existing
	}
	if uid := ParseMetadataUserID(strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String())); uid != nil && uid.SessionID != "" {
		return uid.SessionID
	}
	if promptCacheKey := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); promptCacheKey != "" {
		return promptCacheKey
	}

	seed := deriveAnthropicCursorContentSessionSeed(body)
	if seed == "" {
		return ""
	}
	return GenerateSessionUUID(seed)
}

func InjectAnthropicMetadataUserIDIfMissing(body []byte, userAgent, sessionID string) []byte {
	if len(body) == 0 || strings.TrimSpace(sessionID) == "" {
		return body
	}
	if existing := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String()); existing != "" {
		return body
	}

	deviceSum := sha256.Sum256([]byte("cursor_anthropic_compat:" + sessionID))
	deviceID := hex.EncodeToString(deviceSum[:])
	userID := FormatMetadataUserID(deviceID, "", sessionID, ExtractCLIVersion(userAgent))

	patched, err := sjson.SetBytes(body, "metadata.user_id", userID)
	if err != nil {
		return body
	}
	return patched
}

func ApplyCursorCompatAnthropicSession(c *gin.Context, parsed *ParsedRequest, body []byte) ([]byte, string) {
	sessionID := ResolveCursorCompatAnthropicSessionID(c, body)
	if sessionID == "" {
		return body, ""
	}

	if c != nil && c.Request != nil && strings.TrimSpace(c.GetHeader("X-Claude-Code-Session-Id")) == "" {
		c.Request.Header.Set("X-Claude-Code-Session-Id", sessionID)
	}

	patchedBody := InjectAnthropicMetadataUserIDIfMissing(body, "", sessionID)
	if c != nil {
		patchedBody = InjectAnthropicMetadataUserIDIfMissing(body, c.GetHeader("User-Agent"), sessionID)
	}

	if parsed != nil {
		parsed.Body = patchedBody
		if userID := strings.TrimSpace(gjson.GetBytes(patchedBody, "metadata.user_id").String()); userID != "" {
			parsed.MetadataUserID = userID
		}
	}

	return patchedBody, sessionID
}
