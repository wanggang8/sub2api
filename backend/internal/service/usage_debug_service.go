package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	usageDebugRedisKeyPrefix = "usage_debug:"
	defaultUsageDebugTTL     = 4 * time.Hour
	usageDebugBodyMaxBytes   = 256 * 1024
)

type UsageDebugDetail struct {
	RequestID string    `json:"request_id"`
	CreatedAt time.Time `json:"created_at"`

	RequestHeaders map[string][]string `json:"request_headers,omitempty"`
	RequestBody    string              `json:"request_body,omitempty"`
	RequestBodyTruncated bool          `json:"request_body_truncated,omitempty"`
	RequestBodyBytes *int              `json:"request_body_bytes,omitempty"`

	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	ResponseBody    string              `json:"response_body,omitempty"`
	ResponseBodyTruncated bool          `json:"response_body_truncated,omitempty"`
	ResponseBodyBytes *int              `json:"response_body_bytes,omitempty"`

	UpstreamRequestBody  string `json:"upstream_request_body,omitempty"`
	UpstreamRequestBodyTruncated bool `json:"upstream_request_body_truncated,omitempty"`
	UpstreamRequestBodyBytes *int `json:"upstream_request_body_bytes,omitempty"`
	UpstreamResponseBody string `json:"upstream_response_body,omitempty"`
	UpstreamResponseBodyTruncated bool `json:"upstream_response_body_truncated,omitempty"`
	UpstreamResponseBodyBytes *int `json:"upstream_response_body_bytes,omitempty"`
}

type UsageDebugService struct {
	redisClient *redis.Client
	ttl         time.Duration
}

func NewUsageDebugService(redisClient *redis.Client) *UsageDebugService {
	return &UsageDebugService{
		redisClient: redisClient,
		ttl:         defaultUsageDebugTTL,
	}
}

func (s *UsageDebugService) Get(ctx context.Context, requestID string) (*UsageDebugDetail, error) {
	if s == nil || s.redisClient == nil {
		return nil, nil
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, nil
	}
	raw, err := s.redisClient.Get(ctx, usageDebugRedisKeyPrefix+requestID).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	var detail UsageDebugDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}

func (s *UsageDebugService) Put(ctx context.Context, detail *UsageDebugDetail) error {
	if s == nil || s.redisClient == nil || detail == nil {
		return nil
	}
	requestID := strings.TrimSpace(detail.RequestID)
	if requestID == "" {
		return nil
	}
	if detail.CreatedAt.IsZero() {
		detail.CreatedAt = time.Now().UTC()
	}
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	return s.redisClient.Set(ctx, usageDebugRedisKeyPrefix+requestID, data, s.ttl).Err()
}

func PrepareUsageDebugBody(raw []byte) (body string, truncated bool, bytesLen int) {
	if len(raw) == 0 {
		return "", false, 0
	}
	bytesLen = len(raw)
	if len(raw) > usageDebugBodyMaxBytes {
		return string(raw[:usageDebugBodyMaxBytes]), true, bytesLen
	}
	return string(raw), false, bytesLen
}

func PrepareUsageDebugHeaders(header http.Header) map[string][]string {
	if len(header) == 0 {
		return nil
	}
	out := make(map[string][]string, len(header))
	for key, values := range header {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		switch lowerKey {
		case "authorization", "cookie", "set-cookie", "x-api-key":
			out[key] = []string{"[redacted]"}
		default:
			copied := make([]string, len(values))
			copy(copied, values)
			out[key] = copied
		}
	}
	keys := make([]string, 0, len(out))
	for key := range out {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	normalized := make(map[string][]string, len(out))
	for _, key := range keys {
		normalized[key] = out[key]
	}
	return normalized
}

func UsageDebugRequestKey(upstreamRequestID string) string {
	return resolveUsageBillingRequestID(context.Background(), strings.TrimSpace(upstreamRequestID))
}

func ResolveUsageDebugRequestID(ctx context.Context, upstreamRequestID string) string {
	return resolveUsageBillingRequestID(ctx, strings.TrimSpace(upstreamRequestID))
}

func BuildUsageDebugDetail(
	requestID string,
	requestHeaders http.Header,
	requestBody []byte,
	responseHeaders http.Header,
	responseBody []byte,
	upstreamRequestBody []byte,
	upstreamResponseBody []byte,
) *UsageDebugDetail {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil
	}
	requestBodyText, requestBodyTruncated, requestBodyBytes := PrepareUsageDebugBody(requestBody)
	responseBodyText, responseBodyTruncated, responseBodyBytes := PrepareUsageDebugBody(responseBody)
	upstreamRequestBodyText, upstreamRequestBodyTruncated, upstreamRequestBodyBytes := PrepareUsageDebugBody(upstreamRequestBody)
	upstreamResponseBodyText, upstreamResponseBodyTruncated, upstreamResponseBodyBytes := PrepareUsageDebugBody(upstreamResponseBody)
	return &UsageDebugDetail{
		RequestID:                     requestID,
		RequestHeaders:                PrepareUsageDebugHeaders(requestHeaders),
		RequestBody:                   requestBodyText,
		RequestBodyTruncated:          requestBodyTruncated,
		RequestBodyBytes:              intPtrIfPositive(requestBodyBytes),
		ResponseHeaders:               PrepareUsageDebugHeaders(responseHeaders),
		ResponseBody:                  responseBodyText,
		ResponseBodyTruncated:         responseBodyTruncated,
		ResponseBodyBytes:             intPtrIfPositive(responseBodyBytes),
		UpstreamRequestBody:           upstreamRequestBodyText,
		UpstreamRequestBodyTruncated:  upstreamRequestBodyTruncated,
		UpstreamRequestBodyBytes:      intPtrIfPositive(upstreamRequestBodyBytes),
		UpstreamResponseBody:          upstreamResponseBodyText,
		UpstreamResponseBodyTruncated: upstreamResponseBodyTruncated,
		UpstreamResponseBodyBytes:     intPtrIfPositive(upstreamResponseBodyBytes),
	}
}

func ExtractUsageDebugUpstreamRequestBody(c interface{ Get(string) (any, bool) }) []byte {
	if c == nil {
		return nil
	}
	if raw, ok := c.Get(OpsUpstreamRequestBodyKey); ok {
		switch v := raw.(type) {
		case []byte:
			return v
		case string:
			return []byte(v)
		}
	}
	return nil
}

func ExtractUsageDebugUpstreamResponseBody(c interface{ Get(string) (any, bool) }) []byte {
	body, _, _ := ExtractUsageDebugUpstreamResponseSnapshot(c)
	return body
}

func ExtractUsageDebugUpstreamResponseSnapshot(c interface{ Get(string) (any, bool) }) ([]byte, int, bool) {
	if c == nil {
		return nil, 0, false
	}
	var body []byte
	if raw, ok := c.Get(OpsUpstreamResponseBodyKey); ok {
		switch v := raw.(type) {
		case []byte:
			body = v
		case string:
			body = []byte(v)
		}
	}
	if len(body) == 0 {
		if raw, ok := c.Get(OpsUpstreamErrorDetailKey); ok {
			if s, ok := raw.(string); ok {
				body = []byte(s)
			}
		}
	}
	bytesLen := len(body)
	if raw, ok := c.Get(OpsUpstreamResponseBodyBytesKey); ok {
		switch v := raw.(type) {
		case int:
			bytesLen = v
		case int64:
			bytesLen = int(v)
		}
	}
	truncated := false
	if raw, ok := c.Get(OpsUpstreamResponseBodyTruncatedKey); ok {
		if b, ok := raw.(bool); ok {
			truncated = b
		}
	}
	if len(body) == 0 {
		return nil, bytesLen, truncated
	}
	return body, bytesLen, truncated
}

func ExtractUsageDebugUpstreamRequestSnapshot(c interface{ Get(string) (any, bool) }) ([]byte, int, bool) {
	if c == nil {
		return nil, 0, false
	}
	if raw, ok := c.Get(OpsUpstreamRequestBodyKey); ok {
		if s, ok := raw.(string); ok {
			return []byte(s), len(s), false
		}
		if b, ok := raw.([]byte); ok {
			return b, len(b), false
		}
	}
	return nil, 0, false
}

func intPtrIfPositive(value int) *int {
	if value <= 0 {
		return nil
	}
	v := value
	return &v
}

func (s *UsageDebugService) ExportJSON(ctx context.Context, requestID string) ([]byte, error) {
	detail, err := s.Get(ctx, requestID)
	if err != nil || detail == nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(detail, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal usage debug detail: %w", err)
	}
	return raw, nil
}
