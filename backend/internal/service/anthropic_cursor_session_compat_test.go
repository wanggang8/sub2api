package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveAnthropicCursorContentSessionSeedIncludesInstructions(t *testing.T) {
	seed1 := deriveAnthropicCursorContentSessionSeed([]byte(`{"model":"claude-sonnet","instructions":"alpha","messages":[{"role":"user","content":"hi"}]}`))
	seed2 := deriveAnthropicCursorContentSessionSeed([]byte(`{"model":"claude-sonnet","instructions":"beta","messages":[{"role":"user","content":"hi"}]}`))
	require.NotEmpty(t, seed1)
	require.NotEqual(t, seed1, seed2)
}

func TestDeriveAnthropicCursorContentSessionSeedFallsBackToInput(t *testing.T) {
	seedFromString := deriveAnthropicCursorContentSessionSeed([]byte(`{"model":"claude-sonnet","input":"hello"}`))
	seedFromArray := deriveAnthropicCursorContentSessionSeed([]byte(`{"model":"claude-sonnet","input":[{"type":"input_text","text":"hello"}]}`))
	require.NotEmpty(t, seedFromString)
	require.NotEmpty(t, seedFromArray)
	require.NotEqual(t, seedFromString, deriveAnthropicCursorContentSessionSeed([]byte(`{"model":"claude-sonnet","input":"world"}`)))
	require.NotEqual(t, seedFromArray, deriveAnthropicCursorContentSessionSeed([]byte(`{"model":"claude-sonnet","input":[{"type":"input_text","text":"world"}]}`)))
}
