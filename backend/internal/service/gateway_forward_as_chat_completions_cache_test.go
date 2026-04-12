package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestInjectCursorAnthropicHistoryCacheControl(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"system":"You are Cursor, an expert coding assistant.",
		"messages":[
			{"role":"user","content":"Repository context"},
			{"role":"assistant","content":[{"type":"text","text":"I reviewed the files."}]},
			{"role":"user","content":"Fix the latest error."}
		]
	}`)

	patched := injectCursorAnthropicHistoryCacheControl(body)

	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "system.0.cache_control.type").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "messages.0.content.0.cache_control.type").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "messages.1.content.0.cache_control.type").String())
	require.False(t, gjson.GetBytes(patched, "messages.2.content.0.cache_control").Exists())
}

func TestInjectCursorAnthropicHistoryCacheControl_PreservesExistingCacheControl(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"system":[{"type":"text","text":"System prompt","cache_control":{"type":"ephemeral"}}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"Stable prefix","cache_control":{"type":"ephemeral"}}]},
			{"role":"user","content":"Latest question"}
		]
	}`)

	patched := injectCursorAnthropicHistoryCacheControl(body)

	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "system.0.cache_control.type").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "messages.0.content.0.cache_control.type").String())
	require.False(t, gjson.GetBytes(patched, "messages.1.content.0.cache_control").Exists())
}

func TestInjectCursorAnthropicHistoryCacheControl_LastMergedMessageKeepsStablePrefix(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"messages":[
			{"role":"assistant","content":[{"type":"text","text":"Earlier assistant context"}]},
			{"role":"user","content":[
				{"type":"text","text":"Stable prefix context"},
				{"type":"text","text":"Latest dynamic question"}
			]}
		]
	}`)

	patched := injectCursorAnthropicHistoryCacheControl(body)

	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "messages.1.content.0.cache_control.type").String())
	require.False(t, gjson.GetBytes(patched, "messages.1.content.1.cache_control").Exists())
}

func TestInjectCursorAnthropicHistoryCacheControl_LastTextBlockStaysDynamicEvenWhenImageFollows(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"messages":[
			{"role":"assistant","content":[{"type":"text","text":"Earlier assistant context"}]},
			{"role":"user","content":[
				{"type":"text","text":"Stable prefix context"},
				{"type":"text","text":"Latest dynamic question"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}
			]}
		]
	}`)

	patched := injectCursorAnthropicHistoryCacheControl(body)

	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "messages.1.content.0.cache_control.type").String())
	require.False(t, gjson.GetBytes(patched, "messages.1.content.1.cache_control").Exists())
	require.False(t, gjson.GetBytes(patched, "messages.1.content.2.cache_control").Exists())
}

func TestEnforceCacheControlLimit_PreservesOldestMessagePrefix(t *testing.T) {
	body := []byte(`{
		"system":[{"type":"text","text":"System","cache_control":{"type":"ephemeral"}}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"Oldest prefix","cache_control":{"type":"ephemeral"}}]},
			{"role":"assistant","content":[{"type":"text","text":"Older history","cache_control":{"type":"ephemeral"}}]},
			{"role":"user","content":[{"type":"text","text":"Newer history","cache_control":{"type":"ephemeral"}}]},
			{"role":"assistant","content":[{"type":"text","text":"Newest history","cache_control":{"type":"ephemeral"}}]}
		]
	}`)

	patched := enforceCacheControlLimit(body)

	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "system.0.cache_control.type").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "messages.0.content.0.cache_control.type").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "messages.1.content.0.cache_control.type").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(patched, "messages.2.content.0.cache_control.type").String())
	require.False(t, gjson.GetBytes(patched, "messages.3.content.0.cache_control").Exists())
}
