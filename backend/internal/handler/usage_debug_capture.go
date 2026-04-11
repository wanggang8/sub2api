package handler

import (
	"context"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const usageDebugCaptureLimit = 256 * 1024

type usageDebugRecorder interface {
	Put(ctx context.Context, detail *service.UsageDebugDetail) error
}

type usageDebugCapture interface {
	EnableUsageDebugCapture(limit int)
	UsageDebugBodyBytes() []byte
	UsageDebugBodyCapturedBytes() int
	UsageDebugBodyTruncated() bool
}

func attachUsageDebugCapture(c *gin.Context) usageDebugCapture {
	if c == nil || c.Writer == nil {
		return nil
	}
	if writer, ok := c.Writer.(usageDebugCapture); ok {
		writer.EnableUsageDebugCapture(usageDebugCaptureLimit)
		return writer
	}
	return nil
}

func cloneHeaderOrNil(header http.Header) http.Header {
	if len(header) == 0 {
		return nil
	}
	return header.Clone()
}

func usageDebugBodySnapshot(capture usageDebugCapture) ([]byte, int, bool) {
	if capture == nil {
		return nil, 0, false
	}
	return append([]byte(nil), capture.UsageDebugBodyBytes()...), capture.UsageDebugBodyCapturedBytes(), capture.UsageDebugBodyTruncated()
}

func usageDebugUpstreamResponseSnapshot(c interface{ Get(string) (any, bool) }) ([]byte, int, bool) {
	return service.ExtractUsageDebugUpstreamResponseSnapshot(c)
}

func storeUsageDebugDetail(
	ctx context.Context,
	recorder usageDebugRecorder,
	requestID string,
	requestHeaders http.Header,
	requestBody []byte,
	responseHeaders http.Header,
	responseBody []byte,
	responseBodyBytes int,
	responseBodyTruncated bool,
	upstreamRequestBody []byte,
	upstreamResponseBody []byte,
	upstreamResponseBodyBytes int,
	upstreamResponseBodyTruncated bool,
) {
	if recorder == nil {
		return
	}
	detail := service.BuildUsageDebugDetail(
		requestID,
		requestHeaders,
		requestBody,
		responseHeaders,
		responseBody,
		upstreamRequestBody,
		upstreamResponseBody,
	)
	if detail == nil {
		return
	}
	if responseBodyBytes > 0 {
		detail.ResponseBodyBytes = &responseBodyBytes
		detail.ResponseBodyTruncated = responseBodyTruncated
	}
	if upstreamResponseBodyBytes > 0 {
		detail.UpstreamResponseBodyBytes = &upstreamResponseBodyBytes
		detail.UpstreamResponseBodyTruncated = upstreamResponseBodyTruncated
	}
	_ = recorder.Put(ctx, detail)
}
