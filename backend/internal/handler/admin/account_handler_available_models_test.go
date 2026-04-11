package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type availableModelsAdminService struct {
	*stubAdminService
	account service.Account
}

func (s *availableModelsAdminService) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	if s.account.ID == id {
		acc := s.account
		return &acc, nil
	}
	return s.stubAdminService.GetAccount(context.Background(), id)
}

func setupAvailableModelsRouter(adminSvc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/api/v1/admin/accounts/:id/models", handler.GetAvailableModels)
	router.GET("/api/v1/admin/accounts/:id/models/fetch", handler.FetchModelsFromUpstream)
	router.POST("/api/v1/admin/accounts/fetch-models-preview", handler.FetchModelsPreview)
	return router
}

func TestAccountHandlerGetAvailableModels_OpenAIOAuthUsesExplicitModelMapping(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       42,
			Name:     "openai-oauth",
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeOAuth,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5": "gpt-5.1",
				},
			},
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/42/models", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	require.Equal(t, "gpt-5", resp.Data[0].ID)
}

func TestAccountHandlerFetchModelsPreview_OpenAIBaseURLWithV1Suffix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		require.Equal(t, "Bearer sk-preview", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-preview","object":"model","type":"model","display_name":"GPT Preview"}]}`))
	}))
	defer upstream.Close()

	router := setupAvailableModelsRouter(newStubAdminService())
	body := map[string]any{
		"platform": "openai",
		"type":     "apikey",
		"credentials": map[string]any{
			"base_url": upstream.URL + "/v1",
			"api_key":  "sk-preview",
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/fetch-models-preview", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
			Source string `json:"source"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Models, 1)
	require.Equal(t, "upstream", resp.Data.Source)
	require.Equal(t, "gpt-preview", resp.Data.Models[0].ID)
}

func TestAccountHandlerFetchModelsPreview_UsesRequestCredentialsInsteadOfStoredAccount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		require.Equal(t, "Bearer sk-new", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-new","object":"model","type":"model","display_name":"GPT New"}]}`))
	}))
	defer upstream.Close()

	router := setupAvailableModelsRouter(newStubAdminService())
	body := map[string]any{
		"platform": "openai",
		"type":     "apikey",
		"credentials": map[string]any{
			"base_url": upstream.URL,
			"api_key":  "sk-new",
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/fetch-models-preview", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
			Source string `json:"source"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Models, 1)
	require.Equal(t, "upstream", resp.Data.Source)
	require.Equal(t, "gpt-new", resp.Data.Models[0].ID)
}

func TestAccountHandlerFetchModelsPreview_AnthropicFallsBackToDefaultModels(t *testing.T) {
	router := setupAvailableModelsRouter(newStubAdminService())
	body := map[string]any{
		"platform": "anthropic",
		"type":     "apikey",
		"credentials": map[string]any{
			"base_url": "https://api.anthropic.com",
			"api_key":  "sk-anthropic",
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/fetch-models-preview", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
			Source string `json:"source"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Data.Models)
	require.Equal(t, "default", resp.Data.Source)
	require.Equal(t, claude.DefaultModels[0].ID, resp.Data.Models[0].ID)
}

func TestAccountHandlerFetchModelsFromUpstream_AnthropicFallsBackToDefaultModels(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       42,
			Name:     "anthropic-apikey",
			Platform: service.PlatformAnthropic,
			Type:     service.AccountTypeAPIKey,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"base_url": "https://api.anthropic.com",
				"api_key":  "sk-anthropic",
			},
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/42/models/fetch", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Data)
	require.Equal(t, claude.DefaultModels[0].ID, resp.Data[0].ID)
}
