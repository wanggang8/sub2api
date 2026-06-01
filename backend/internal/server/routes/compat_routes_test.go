package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCompatRoutesRegisterCursorOnly(t *testing.T) {
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
	routes := router.Routes()

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/cursor/v1/chat/completions"},
		{method: http.MethodPost, path: "/cursor/v1/responses"},
		{method: http.MethodPost, path: "/cursor/v1/messages"},
		{method: http.MethodPost, path: "/cursor/v1/messages/count_tokens"},
		{method: http.MethodGet, path: "/cursor/v1/models"},
	} {
		require.True(t, compatRouteExists(routes, tc.method, tc.path), "%s %s should be registered", tc.method, tc.path)
	}

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/chat-stream"},
		{method: http.MethodGet, path: "/usage/api/get-models"},
		{method: http.MethodGet, path: "/usage/api/balance"},
		{method: http.MethodGet, path: "/usage/api/getLoginToken"},
	} {
		require.False(t, compatRouteExists(routes, tc.method, tc.path), "%s %s should not be registered after Augment removal", tc.method, tc.path)
	}
}

func compatRouteExists(routes gin.RoutesInfo, method, path string) bool {
	for _, route := range routes {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}
