package service

import (
	"context"
	"net/http"
)

type upstreamRequestOptionsKey struct{}

type UpstreamRequestOptions struct {
	SkipTLSVerify bool
}

func WithUpstreamRequestOptions(ctx context.Context, opts UpstreamRequestOptions) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, upstreamRequestOptionsKey{}, opts)
}

func UpstreamRequestOptionsFromContext(ctx context.Context) UpstreamRequestOptions {
	if ctx == nil {
		return UpstreamRequestOptions{}
	}
	if opts, ok := ctx.Value(upstreamRequestOptionsKey{}).(UpstreamRequestOptions); ok {
		return opts
	}
	return UpstreamRequestOptions{}
}

func UpstreamRequestSkipsTLSVerify(req *http.Request) bool {
	if req == nil {
		return false
	}
	return UpstreamRequestOptionsFromContext(req.Context()).SkipTLSVerify
}

func ApplyAccountUpstreamRequestOptions(req *http.Request, account *Account) *http.Request {
	if req == nil || account == nil || !account.ShouldSkipTLSVerify() {
		return req
	}
	opts := UpstreamRequestOptionsFromContext(req.Context())
	opts.SkipTLSVerify = true
	return req.WithContext(WithUpstreamRequestOptions(req.Context(), opts))
}
