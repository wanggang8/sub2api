package cursor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const anthropicContentSessionSeedPrefix = "compat_as_"

func isOpenAICompatPath(path string) bool {
	normalized := strings.TrimRight(strings.TrimSpace(path), "/")
	return normalized == "/cursor/v1/responses" || normalized == "/cursor/v1/chat/completions"
}

func isAnthropicCompatPath(path string) bool {
	normalized := strings.TrimRight(strings.TrimSpace(path), "/")
	switch normalized {
	case "/cursor/v1/messages", "/cursor/v1/chat/completions", "/cursor/v1/responses":
		return true
	default:
		return false
	}
}

func ResolveOpenAIPromptCacheKey(c *gin.Context, body []byte) string {
	if c == nil || c.Request == nil || !isOpenAICompatPath(c.Request.URL.Path) {
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
	if uid := service.ParseMetadataUserID(strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String())); uid != nil && uid.SessionID != "" {
		return uid.SessionID
	}
	seed := deriveOpenAIContentSessionSeed(body)
	if seed == "" {
		return ""
	}
	return service.GenerateSessionUUID(seed)
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

func ResolveAnthropicSessionID(c *gin.Context, body []byte) string {
	if c == nil || c.Request == nil || !isAnthropicCompatPath(c.Request.URL.Path) {
		return ""
	}
	if existing := strings.TrimSpace(c.GetHeader("X-Claude-Code-Session-Id")); existing != "" {
		return existing
	}
	if existing := strings.TrimSpace(c.GetHeader("session_id")); existing != "" {
		return existing
	}
	if uid := service.ParseMetadataUserID(strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String())); uid != nil && uid.SessionID != "" {
		return uid.SessionID
	}
	if promptCacheKey := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); promptCacheKey != "" {
		return promptCacheKey
	}
	seed := deriveAnthropicContentSessionSeed(body)
	if seed == "" {
		return ""
	}
	return service.GenerateSessionUUID(seed)
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
	userID := service.FormatMetadataUserID(deviceID, "", sessionID, service.ExtractCLIVersion(userAgent))
	patched, err := sjson.SetBytes(body, "metadata.user_id", userID)
	if err != nil {
		return body
	}
	return patched
}

func ApplyAnthropicSession(c *gin.Context, body []byte) ([]byte, string) {
	sessionID := ResolveAnthropicSessionID(c, body)
	if sessionID == "" {
		return body, ""
	}
	if c != nil && c.Request != nil && strings.TrimSpace(c.GetHeader("X-Claude-Code-Session-Id")) == "" {
		c.Request.Header.Set("X-Claude-Code-Session-Id", sessionID)
	}
	if c != nil {
		body = InjectAnthropicMetadataUserIDIfMissing(body, c.GetHeader("User-Agent"), sessionID)
	}
	return body, sessionID
}

func deriveOpenAIContentSessionSeed(body []byte) string {
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
		_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(tools.Raw)))
	}
	if funcs := gjson.GetBytes(body, "functions"); funcs.Exists() && funcs.IsArray() && funcs.Raw != "[]" {
		_, _ = b.WriteString("|functions=")
		_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(funcs.Raw)))
	}
	if instr := gjson.GetBytes(body, "instructions").String(); instr != "" {
		_, _ = b.WriteString("|instructions=")
		_, _ = b.WriteString(instr)
	}

	firstUserCaptured := false
	msgs := gjson.GetBytes(body, "messages")
	if msgs.Exists() && msgs.IsArray() {
		msgs.ForEach(func(_, msg gjson.Result) bool {
			role := msg.Get("role").String()
			switch role {
			case "system", "developer":
				_, _ = b.WriteString("|system=")
				if c := msg.Get("content"); c.Exists() {
					_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(c.Raw)))
				}
			case "user":
				if !firstUserCaptured {
					_, _ = b.WriteString("|first_user=")
					if c := msg.Get("content"); c.Exists() {
						_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(c.Raw)))
					}
					firstUserCaptured = true
				}
			}
			return true
		})
	} else if inp := gjson.GetBytes(body, "input"); inp.Exists() {
		if inp.Type == gjson.String {
			_, _ = b.WriteString("|input=")
			_, _ = b.WriteString(inp.String())
		} else if inp.IsArray() {
			inp.ForEach(func(_, item gjson.Result) bool {
				role := item.Get("role").String()
				switch role {
				case "system", "developer":
					_, _ = b.WriteString("|system=")
					if c := item.Get("content"); c.Exists() {
						_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(c.Raw)))
					}
				case "user":
					if !firstUserCaptured {
						_, _ = b.WriteString("|first_user=")
						if c := item.Get("content"); c.Exists() {
							_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(c.Raw)))
						}
						firstUserCaptured = true
					}
				}
				if !firstUserCaptured && item.Get("type").String() == "input_text" {
					_, _ = b.WriteString("|first_user=")
					if text := item.Get("text").String(); text != "" {
						_, _ = b.WriteString(text)
					}
					firstUserCaptured = true
				}
				return true
			})
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "compat_cs_" + b.String()
}

func deriveAnthropicContentSessionSeed(body []byte) string {
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
		_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(tools.Raw)))
	}
	if system := gjson.GetBytes(body, "system"); system.Exists() {
		_, _ = b.WriteString("|system=")
		_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(system.Raw)))
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
				if content := msg.Get("content"); content.Exists() {
					_, _ = b.WriteString("|system=")
					_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(content.Raw)))
				}
			case "user":
				if !firstUserCaptured {
					if content := msg.Get("content"); content.Exists() {
						_, _ = b.WriteString("|first_user=")
						_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(content.Raw)))
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
							_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(content.Raw)))
						}
						return true
					}
					if role == "user" && !firstUserCaptured {
						if content := item.Get("content"); content.Exists() {
							_, _ = b.WriteString("|first_user=")
							_, _ = b.WriteString(normalizeSeedJSON(json.RawMessage(content.Raw)))
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

func normalizeSeedJSON(v json.RawMessage) string {
	if len(v) == 0 {
		return ""
	}
	var tmp any
	if err := json.Unmarshal(v, &tmp); err != nil {
		return string(v)
	}
	out, err := json.Marshal(tmp)
	if err != nil {
		return string(v)
	}
	return string(out)
}
