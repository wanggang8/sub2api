package service

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

// HTTPUpstream 上游 HTTP 请求接口
// 用于向上游 API（Claude、OpenAI、Gemini 等）发送请求
type HTTPUpstream interface {
	// Do 执行 HTTP 请求（不启用 TLS 指纹）
	Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error)

	// DoWithTLS 执行带 TLS 选项的 HTTP 请求。
	//
	// options 为 nil 时，行为与 Do 方法相同。
	DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, options *UpstreamTLSOptions) (*http.Response, error)
}

// UpstreamTLSOptions describes per-account TLS behavior for upstream requests.
type UpstreamTLSOptions struct {
	FingerprintProfile *tlsfingerprint.Profile
	InsecureSkipVerify bool
}
