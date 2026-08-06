package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/observercontrol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterObserverRoutesOverwritesResolvedClientIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	RegisterObserverRoutes(router, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Observed-IP", request.Header.Get(observercontrol.ResolvedClientIPHeader))
		writer.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/observer/agents/heartbeat", nil)
	request.RemoteAddr = "192.0.2.44:1234"
	request.Header.Set(observercontrol.ResolvedClientIPHeader, "203.0.113.99")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, "192.0.2.44", recorder.Header().Get("X-Observed-IP"))
}
