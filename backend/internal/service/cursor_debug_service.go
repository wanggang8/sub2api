package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
)

const cursorDebugRecordIDKey = "cursor_debug_record_id"

var (
	defaultCursorDebugService     = NewCursorDebugService(DefaultCursorDebugConfig())
	defaultCursorDebugIDCounter   atomic.Uint64
	defaultCursorDebugServiceLock sync.RWMutex
)

type CursorDebugConfig struct {
	Enabled        bool `json:"enabled"`
	MaxRecords     int  `json:"max_records"`
	MaxBodyBytes   int  `json:"max_body_bytes"`
	RetentionHours int  `json:"retention_hours"`
}

type CursorDebugBody struct {
	Body      string `json:"body"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated"`
}

type CursorDebugRecord struct {
	ID              string          `json:"id"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	Method          string          `json:"method,omitempty"`
	Path            string          `json:"path,omitempty"`
	Model           string          `json:"model,omitempty"`
	Platform        string          `json:"platform,omitempty"`
	Stream          bool            `json:"stream"`
	RequestID       string          `json:"request_id,omitempty"`
	StatusCode      int             `json:"status_code,omitempty"`
	RawRequest      CursorDebugBody `json:"raw_request_body"`
	Normalized      CursorDebugBody `json:"normalized_request_body"`
	UpstreamRequest CursorDebugBody `json:"upstream_request_body"`
	RawResponse     CursorDebugBody `json:"raw_upstream_response_body"`
	FinalResponse   CursorDebugBody `json:"final_response_body"`
}

type CursorDebugListResult struct {
	Items    []CursorDebugRecord `json:"items"`
	Total    int                 `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"page_size"`
}

type CursorDebugConfigUpdate struct {
	Enabled        *bool `json:"enabled"`
	MaxRecords     *int  `json:"max_records"`
	MaxBodyBytes   *int  `json:"max_body_bytes"`
	RetentionHours *int  `json:"retention_hours"`
}

type CursorDebugRecordPatch struct {
	Model                  string
	Platform               string
	Stream                 *bool
	StatusCode             int
	UpstreamRequestBody    []byte
	RawResponseBody        []byte
	RawResponseTruncated   bool
	FinalResponseBody      []byte
	FinalResponseTruncated bool
}

type CursorDebugService struct {
	mu      sync.Mutex
	config  CursorDebugConfig
	records []CursorDebugRecord
	byID    map[string]int
}

func DefaultCursorDebugConfig() CursorDebugConfig {
	return CursorDebugConfig{
		Enabled:        false,
		MaxRecords:     100,
		MaxBodyBytes:   512 * 1024,
		RetentionHours: 6,
	}
}

func NewCursorDebugService(cfg CursorDebugConfig) *CursorDebugService {
	cfg = normalizeCursorDebugConfig(cfg)
	return &CursorDebugService{config: cfg, byID: make(map[string]int)}
}

func DefaultCursorDebugService() *CursorDebugService {
	defaultCursorDebugServiceLock.RLock()
	defer defaultCursorDebugServiceLock.RUnlock()
	return defaultCursorDebugService
}

func SetDefaultCursorDebugServiceForTest(s *CursorDebugService) func() {
	defaultCursorDebugServiceLock.Lock()
	prev := defaultCursorDebugService
	if s == nil {
		s = NewCursorDebugService(DefaultCursorDebugConfig())
	}
	defaultCursorDebugService = s
	defaultCursorDebugServiceLock.Unlock()
	return func() {
		defaultCursorDebugServiceLock.Lock()
		defaultCursorDebugService = prev
		defaultCursorDebugServiceLock.Unlock()
	}
}

func (s *CursorDebugService) GetConfig() CursorDebugConfig {
	if s == nil {
		return DefaultCursorDebugConfig()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config
}

func (s *CursorDebugService) UpdateConfig(update CursorDebugConfigUpdate) CursorDebugConfig {
	if s == nil {
		return DefaultCursorDebugConfig()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.config
	if update.Enabled != nil {
		cfg.Enabled = *update.Enabled
	}
	if update.MaxRecords != nil {
		cfg.MaxRecords = *update.MaxRecords
	}
	if update.MaxBodyBytes != nil {
		cfg.MaxBodyBytes = *update.MaxBodyBytes
	}
	if update.RetentionHours != nil {
		cfg.RetentionHours = *update.RetentionHours
	}
	s.config = normalizeCursorDebugConfig(cfg)
	s.pruneLocked(time.Now())
	return s.config
}

func (s *CursorDebugService) Clear() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.records)
	s.records = nil
	s.byID = make(map[string]int)
	return n
}

func (s *CursorDebugService) Begin(c *gin.Context, raw, normalized []byte) string {
	if s == nil || c == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Enabled {
		return ""
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("%d-%06d", now.UnixNano(), defaultCursorDebugIDCounter.Add(1))
	record := CursorDebugRecord{
		ID:         id,
		CreatedAt:  now,
		UpdatedAt:  now,
		Method:     requestMethod(c),
		Path:       requestPath(c),
		RequestID:  requestID(c),
		RawRequest: makeCursorDebugBody(raw, s.config.MaxBodyBytes, false),
		Normalized: makeCursorDebugBody(normalized, s.config.MaxBodyBytes, false),
	}
	s.records = append([]CursorDebugRecord{record}, s.records...)
	c.Set(cursorDebugRecordIDKey, id)
	s.reindexLocked()
	s.pruneLocked(now)
	return id
}

func (s *CursorDebugService) Update(c *gin.Context, patch CursorDebugRecordPatch) {
	if s == nil || c == nil {
		return
	}
	id, ok := c.Get(cursorDebugRecordIDKey)
	if !ok {
		return
	}
	idStr := strings.TrimSpace(fmt.Sprint(id))
	if idStr == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, ok := s.byID[idStr]
	if !ok || idx < 0 || idx >= len(s.records) {
		return
	}
	rec := &s.records[idx]
	rec.UpdatedAt = time.Now().UTC()
	if patch.Model != "" {
		rec.Model = strings.TrimSpace(patch.Model)
	}
	if patch.Platform != "" {
		rec.Platform = strings.TrimSpace(patch.Platform)
	}
	if patch.Stream != nil {
		rec.Stream = *patch.Stream
	}
	if patch.StatusCode > 0 {
		rec.StatusCode = patch.StatusCode
	}
	if patch.UpstreamRequestBody != nil {
		rec.UpstreamRequest = makeCursorDebugBody(patch.UpstreamRequestBody, s.config.MaxBodyBytes, false)
	}
	if patch.RawResponseBody != nil {
		rec.RawResponse = appendCursorDebugBody(rec.RawResponse, patch.RawResponseBody, s.config.MaxBodyBytes, patch.RawResponseTruncated)
	}
	if patch.FinalResponseBody != nil {
		rec.FinalResponse = appendCursorDebugBody(rec.FinalResponse, patch.FinalResponseBody, s.config.MaxBodyBytes, patch.FinalResponseTruncated)
	}
}

func (s *CursorDebugService) List(page, pageSize int) CursorDebugListResult {
	if s == nil {
		return CursorDebugListResult{Items: []CursorDebugRecord{}, Page: 1, PageSize: 20}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	total := len(s.records)
	start := (page - 1) * pageSize
	if start >= total {
		return CursorDebugListResult{Items: []CursorDebugRecord{}, Total: total, Page: page, PageSize: pageSize}
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	items := append([]CursorDebugRecord(nil), s.records[start:end]...)
	return CursorDebugListResult{Items: items, Total: total, Page: page, PageSize: pageSize}
}

func (s *CursorDebugService) Get(id string) (CursorDebugRecord, bool) {
	if s == nil {
		return CursorDebugRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(time.Now())
	idx, ok := s.byID[strings.TrimSpace(id)]
	if !ok || idx < 0 || idx >= len(s.records) {
		return CursorDebugRecord{}, false
	}
	return s.records[idx], true
}

func CaptureCursorDebugUpstreamRequest(c *gin.Context) {
	if c == nil {
		return
	}
	raw, ok := c.Get(OpsUpstreamRequestBodyKey)
	if !ok {
		return
	}
	body := anyToBytes(raw)
	if len(body) == 0 {
		return
	}
	DefaultCursorDebugService().Update(c, CursorDebugRecordPatch{UpstreamRequestBody: body})
}

func CaptureCursorDebugResponse(c *gin.Context, rawBody []byte, rawTruncated bool, finalBody []byte, finalTruncated bool, statusCode int) {
	DefaultCursorDebugService().Update(c, CursorDebugRecordPatch{
		StatusCode:             statusCode,
		RawResponseBody:        rawBody,
		RawResponseTruncated:   rawTruncated,
		FinalResponseBody:      finalBody,
		FinalResponseTruncated: finalTruncated,
	})
}

func CaptureCursorDebugFinalError(c *gin.Context, statusCode int, body []byte) {
	DefaultCursorDebugService().Update(c, CursorDebugRecordPatch{
		StatusCode:        statusCode,
		FinalResponseBody: body,
	})
}

func normalizeCursorDebugConfig(cfg CursorDebugConfig) CursorDebugConfig {
	if cfg.MaxRecords <= 0 {
		cfg.MaxRecords = 100
	}
	if cfg.MaxRecords > 1000 {
		cfg.MaxRecords = 1000
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = 512 * 1024
	}
	if cfg.MaxBodyBytes > 4*1024*1024 {
		cfg.MaxBodyBytes = 4 * 1024 * 1024
	}
	if cfg.RetentionHours <= 0 {
		cfg.RetentionHours = 6
	}
	if cfg.RetentionHours > 168 {
		cfg.RetentionHours = 168
	}
	return cfg
}

func (s *CursorDebugService) pruneLocked(now time.Time) {
	if s.byID == nil {
		s.byID = make(map[string]int)
	}
	cutoff := now.Add(-time.Duration(s.config.RetentionHours) * time.Hour)
	filtered := s.records[:0]
	for i := range s.records {
		if len(filtered) >= s.config.MaxRecords {
			continue
		}
		if s.records[i].CreatedAt.Before(cutoff) {
			continue
		}
		filtered = append(filtered, s.records[i])
	}
	s.records = filtered
	s.reindexLocked()
}

func (s *CursorDebugService) reindexLocked() {
	s.byID = make(map[string]int, len(s.records))
	for i := range s.records {
		s.byID[s.records[i].ID] = i
	}
}

func makeCursorDebugBody(body []byte, limit int, alreadyTruncated bool) CursorDebugBody {
	out := CursorDebugBody{Bytes: len(body), Truncated: alreadyTruncated}
	if limit <= 0 {
		limit = 1
	}
	if len(body) > limit {
		out.Body = string(body[:limit])
		out.Truncated = true
		return out
	}
	out.Body = string(body)
	return out
}

func appendCursorDebugBody(existing CursorDebugBody, chunk []byte, limit int, chunkTruncated bool) CursorDebugBody {
	if len(chunk) == 0 && !chunkTruncated {
		return existing
	}
	if limit <= 0 {
		limit = 1
	}
	existing.Bytes += len(chunk)
	existing.Truncated = existing.Truncated || chunkTruncated
	remaining := limit - len(existing.Body)
	if remaining <= 0 {
		existing.Truncated = existing.Truncated || len(chunk) > 0
		return existing
	}
	if len(chunk) > remaining {
		existing.Body += string(chunk[:remaining])
		existing.Truncated = true
		return existing
	}
	existing.Body += string(chunk)
	return existing
}

func anyToBytes(v any) []byte {
	switch typed := v.(type) {
	case nil:
		return nil
	case []byte:
		return typed
	case string:
		return []byte(typed)
	default:
		b, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		return b
	}
}

func requestMethod(c *gin.Context) string {
	if c != nil && c.Request != nil {
		return strings.TrimSpace(c.Request.Method)
	}
	return http.MethodPost
}

func requestPath(c *gin.Context) string {
	if c != nil && c.Request != nil && c.Request.URL != nil {
		return strings.TrimSpace(c.Request.URL.Path)
	}
	return ""
}

func requestID(c *gin.Context) string {
	if c == nil || c.Request == nil {
		return ""
	}
	if v, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(c.GetHeader("X-Request-ID"))
}
