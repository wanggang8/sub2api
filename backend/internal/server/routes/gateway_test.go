package routes

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newGatewayRoutesTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
		},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			c.Next()
		}),
		nil,
		nil,
		nil,
		nil,
		&config.Config{},
	)

	return router
}

func TestGatewayRoutesOpenAIResponsesCompactPathIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{"/v1/responses/compact", "/responses/compact"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI responses handler", path)
	}
}

func TestGatewayRoutesStandardPathsRemainRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()
	routes := router.Routes()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{
			name:   "anthropic messages path",
			method: http.MethodPost,
			path:   "/v1/messages",
		},
		{
			name:   "responses path",
			method: http.MethodPost,
			path:   "/v1/responses",
		},
		{
			name:   "responses subpath wildcard",
			method: http.MethodPost,
			path:   "/v1/responses/*subpath",
		},
		{
			name:   "responses websocket path",
			method: http.MethodGet,
			path:   "/v1/responses",
		},
		{
			name:   "chat completions path",
			method: http.MethodPost,
			path:   "/v1/chat/completions",
		},
		{
			name:   "models path",
			method: http.MethodGet,
			path:   "/v1/models",
		},
		{
			name:   "usage path",
			method: http.MethodGet,
			path:   "/v1/usage",
		},
		{
			name:   "gemini list models path",
			method: http.MethodGet,
			path:   "/v1beta/models",
		},
		{
			name:   "gemini get model path",
			method: http.MethodGet,
			path:   "/v1beta/models/:model",
		},
		{
			name:   "gemini model action path",
			method: http.MethodPost,
			path:   "/v1beta/models/*modelAction",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, hasRoute(routes, tc.method, tc.path), "%s %s should remain registered", tc.method, tc.path)
		})
	}
}

func hasRoute(routes gin.RoutesInfo, method string, path string) bool {
	return slices.ContainsFunc(routes, func(route gin.RouteInfo) bool {
		return route.Method == method && route.Path == path
	})
}
