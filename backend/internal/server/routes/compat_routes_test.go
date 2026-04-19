package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newCompatRoutesTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{
		Gateway:       &handler.GatewayHandler{},
		OpenAIGateway: &handler.OpenAIGatewayHandler{},
	}
	apiKeyAuth := servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		c.Next()
	})

	RegisterCompatRoutes(router, handlers, apiKeyAuth, nil, nil, nil, nil, &config.Config{})
	return router
}

func TestCompatRoutesRemainRegistered(t *testing.T) {
	router := newCompatRoutesTestRouter()
	routes := router.Routes()

	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/cursor/v1/chat/completions"},
		{method: http.MethodPost, path: "/cursor/v1/responses"},
		{method: http.MethodPost, path: "/cursor/v1/messages"},
		{method: http.MethodPost, path: "/cursor/v1/messages/count_tokens"},
		{method: http.MethodGet, path: "/cursor/v1/models"},
		{method: http.MethodPost, path: "/chat-stream"},
		{method: http.MethodGet, path: "/usage/api/get-models"},
		{method: http.MethodGet, path: "/usage/api/balance"},
		{method: http.MethodGet, path: "/usage/api/getLoginToken"},
	}

	for _, tc := range tests {
		require.True(t, hasRoute(routes, tc.method, tc.path), "%s %s should remain registered", tc.method, tc.path)
	}
}

func TestCompatRoutesUseProtocolErrorSchemaOnAuthFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	apiKeyRepo := &compatRouteAPIKeyRepoStub{}
	apiKeyService := service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyAuth := servermiddleware.NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)

	RegisterCompatRoutes(router, handlers, apiKeyAuth, apiKeyService, nil, nil, nil, cfg)

	t.Run("cursor", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", nil)
		req.Header.Set("Authorization", "Bearer invalid")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.JSONEq(t, `{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`, rec.Body.String())
	})

	t.Run("augment_root", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/chat-stream", nil)
		req.Header.Set("Authorization", "Bearer invalid")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		require.JSONEq(t, `{"success":false,"error":"Invalid API key"}`, rec.Body.String())
	})

}

func TestCompatRoutesRejectUngroupedKeyWithProtocolSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	apiKey := &service.APIKey{
		ID:     1,
		Key:    "compat-key",
		Status: service.StatusActive,
		User:   &service.User{ID: 1, Status: service.StatusActive, Balance: 100},
	}
	apiKeyRepo := &compatRouteAPIKeyRepoStub{apiKey: apiKey}
	apiKeyService := service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	settingService := service.NewSettingService(&compatRouteSettingRepoStub{values: map[string]string{
		service.SettingKeyAllowUngroupedKeyScheduling: "false",
	}}, cfg)
	apiKeyAuth := servermiddleware.NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)

	RegisterCompatRoutes(router, handlers, apiKeyAuth, apiKeyService, nil, nil, settingService, cfg)

	t.Run("cursor", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/cursor/v1/responses", nil)
		req.Header.Set("Authorization", "Bearer compat-key")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.JSONEq(t, `{"error":{"message":"API Key is not assigned to any group and cannot be used. Please contact the administrator to assign it to a group.","type":"invalid_request_error"}}`, rec.Body.String())
	})

	t.Run("augment_root", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/chat-stream", nil)
		req.Header.Set("Authorization", "Bearer compat-key")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.JSONEq(t, `{"success":false,"error":"API Key is not assigned to any group and cannot be used. Please contact the administrator to assign it to a group."}`, rec.Body.String())
	})

}

func TestCompatRoutesSkipBillingForAugmentUsagePaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	apiKey := &service.APIKey{
		ID:     1,
		Key:    "augment-expired",
		Status: service.StatusAPIKeyExpired,
		User:   &service.User{ID: 1, Status: service.StatusActive, Balance: 100},
	}
	apiKeyRepo := &compatRouteAPIKeyRepoStub{apiKey: apiKey}
	apiKeyService := service.NewAPIKeyService(apiKeyRepo, nil, nil, nil, nil, nil, cfg)
	apiKeyAuth := servermiddleware.NewAPIKeyAuthMiddleware(apiKeyService, nil, cfg)

	RegisterCompatRoutes(router, handlers, apiKeyAuth, apiKeyService, nil, nil, nil, cfg)

	t.Run("root_get_models_allows_expired_key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/usage/api/get-models", nil)
		req.Header.Set("Authorization", "Bearer augment-expired")
		router.ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusForbidden, rec.Code)
		require.NotEqual(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("root_balance_allows_expired_key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/usage/api/balance", nil)
		req.Header.Set("Authorization", "Bearer augment-expired")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), `"success":true`)
		require.Contains(t, rec.Body.String(), `"status_text":"expired"`)
	})

	t.Run("root_login_token_allows_expired_key", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "https://proxy.example.com/usage/api/getLoginToken", nil)
		req.Header.Set("Authorization", "Bearer augment-expired")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Host", "proxy.example.com")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), `"success":true`)
		require.Contains(t, rec.Body.String(), `"accessToken":"augment-expired"`)
	})

	t.Run("chat_stream_still_enforces_billing", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/chat-stream", nil)
		req.Header.Set("Authorization", "Bearer augment-expired")
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.JSONEq(t, `{"success":false,"error":"API key 已过期"}`, rec.Body.String())
	})
}

func hasRoute(routes gin.RoutesInfo, method, path string) bool {
	for _, route := range routes {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}

type compatRouteAPIKeyRepoStub struct {
	apiKey *service.APIKey
}

func (r *compatRouteAPIKeyRepoStub) Create(ctx context.Context, key *service.APIKey) error {
	return errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) GetByID(ctx context.Context, id int64) (*service.APIKey, error) {
	return nil, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	return "", 0, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) GetByKey(ctx context.Context, key string) (*service.APIKey, error) {
	if r.apiKey != nil && r.apiKey.Key == key {
		clone := *r.apiKey
		return &clone, nil
	}
	return nil, service.ErrAPIKeyNotFound
}

func (r *compatRouteAPIKeyRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	return r.GetByKey(ctx, key)
}

func (r *compatRouteAPIKeyRepoStub) Update(ctx context.Context, key *service.APIKey) error {
	return errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	return nil, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) ExistsByKey(ctx context.Context, key string) (bool, error) {
	return false, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]service.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]service.APIKey, error) {
	return nil, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	return nil, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	return nil, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	return 0, errors.New("not implemented")
}

func (r *compatRouteAPIKeyRepoStub) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	return nil
}

func (r *compatRouteAPIKeyRepoStub) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	return nil
}

func (r *compatRouteAPIKeyRepoStub) ResetRateLimitWindows(ctx context.Context, id int64) error {
	return nil
}

func (r *compatRouteAPIKeyRepoStub) GetRateLimitData(ctx context.Context, id int64) (*service.APIKeyRateLimitData, error) {
	return nil, nil
}

type compatRouteSettingRepoStub struct {
	values map[string]string
}

func (r *compatRouteSettingRepoStub) Get(ctx context.Context, key string) (*service.Setting, error) {
	if value, ok := r.values[key]; ok {
		return &service.Setting{Key: key, Value: value}, nil
	}
	return nil, service.ErrSettingNotFound
}

func (r *compatRouteSettingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func (r *compatRouteSettingRepoStub) Set(ctx context.Context, key, value string) error {
	return errors.New("not implemented")
}

func (r *compatRouteSettingRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	return nil, errors.New("not implemented")
}

func (r *compatRouteSettingRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	return errors.New("not implemented")
}

func (r *compatRouteSettingRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	return nil, errors.New("not implemented")
}

func (r *compatRouteSettingRepoStub) Delete(ctx context.Context, key string) error {
	return errors.New("not implemented")
}
