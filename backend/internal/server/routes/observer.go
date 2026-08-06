package routes

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/observercontrol"
	"github.com/gin-gonic/gin"
)

// RegisterObserverRoutes delegates the isolated observer API surface to its
// standard-library handler while preserving Gin's trusted-proxy IP decision.
func RegisterObserverRoutes(router *gin.Engine, handler http.Handler) {
	router.Any("/api/v1/observer/*path", func(ctx *gin.Context) {
		ctx.Request.Header.Del(observercontrol.ResolvedClientIPHeader)
		ctx.Request.Header.Set(observercontrol.ResolvedClientIPHeader, ctx.ClientIP())
		handler.ServeHTTP(ctx.Writer, ctx.Request)
	})
}
