package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildUsageDebugDetail_RedactsSensitiveHeaders(t *testing.T) {
	headers := http.Header{
		"Authorization": {"Bearer secret"},
		"Cookie":        {"a=b"},
		"X-Test":        {"ok"},
	}

	detail := BuildUsageDebugDetail(
		"req-1",
		headers,
		[]byte(`{"foo":"bar"}`),
		nil,
		nil,
		nil,
		nil,
	)

	require.NotNil(t, detail)
	require.Equal(t, []string{"[redacted]"}, detail.RequestHeaders["Authorization"])
	require.Equal(t, []string{"[redacted]"}, detail.RequestHeaders["Cookie"])
	require.Equal(t, []string{"ok"}, detail.RequestHeaders["X-Test"])
	require.Equal(t, `{"foo":"bar"}`, detail.RequestBody)
}

func TestInjectUsageDebugBody_TruncatesLargePayload(t *testing.T) {
	large := make([]byte, usageDebugBodyMaxBytes+10)
	for i := range large {
		large[i] = 'a'
	}

	got, truncated, bytesLen := PrepareUsageDebugBody(large)
	require.Len(t, got, usageDebugBodyMaxBytes)
	require.True(t, truncated)
	require.Equal(t, len(large), bytesLen)
}

func TestBuildUsageDebugDetail_TracksTruncationAndUpstreamResponse(t *testing.T) {
	large := make([]byte, usageDebugBodyMaxBytes+10)
	for i := range large {
		large[i] = 'b'
	}

	detail := BuildUsageDebugDetail(
		"req-2",
		nil,
		large,
		nil,
		[]byte(`{"client":"response"}`),
		nil,
		[]byte(`{"upstream":"response"}`),
	)

	require.NotNil(t, detail)
	require.True(t, detail.RequestBodyTruncated)
	require.NotNil(t, detail.RequestBodyBytes)
	require.Equal(t, len(large), *detail.RequestBodyBytes)
	require.Equal(t, `{"upstream":"response"}`, detail.UpstreamResponseBody)
	require.NotNil(t, detail.UpstreamResponseBodyBytes)
	require.Equal(t, len(`{"upstream":"response"}`), *detail.UpstreamResponseBodyBytes)
}
