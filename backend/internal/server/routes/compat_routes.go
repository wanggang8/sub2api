package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func RegisterCursorCompatRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
) {
	if r == nil || h == nil || h.CursorCompat == nil {
		return
	}

	middlewars := compatRouteMiddlewares(apiKeyAuth, opsService, cfg)
	if apiKeyService != nil {
		middlewars = compatRouteMiddlewares(
			middleware.NewAPIKeyAuthMiddlewareWithWriter(apiKeyService, subscriptionService, cfg, middleware.CursorAuthErrorWriter),
			opsService,
			cfg,
		)
	}
	if settingService != nil {
		middlewars = append(middlewars, middleware.RequireGroupAssignment(settingService, middleware.CursorErrorWriter))
	}
	group := r.Group("/cursor/v1")
	group.Use(middlewars...)
	{
		group.POST("/chat/completions", h.CursorCompat.ChatCompletions)
		group.POST("/responses", h.CursorCompat.Responses)
		group.POST("/messages", h.CursorCompat.Messages)
		group.POST("/messages/count_tokens", h.CursorCompat.CountTokens)
		group.GET("/models", h.CursorCompat.Models)
	}
}

func RegisterAugmentCompatRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
) {
	if r == nil || h == nil || h.AugmentCompat == nil {
		return
	}

	middlewars := compatRouteMiddlewares(apiKeyAuth, opsService, cfg)
	if apiKeyService != nil {
		middlewars = compatRouteMiddlewares(
			middleware.NewAPIKeyAuthMiddlewareWithWriter(apiKeyService, subscriptionService, cfg, middleware.AugmentAuthErrorWriter),
			opsService,
			cfg,
		)
	}
	if settingService != nil {
		middlewars = append(middlewars, middleware.RequireGroupAssignment(settingService, middleware.AugmentErrorWriter))
	}
	r.POST("/chat-stream", append(middlewars, h.AugmentCompat.ChatStream)...)
	r.GET("/usage/api/get-models", append(middlewars, h.AugmentCompat.GetModels)...)
	r.GET("/usage/api/balance", append(middlewars, h.AugmentCompat.GetBalance)...)
	r.GET("/usage/api/getLoginToken", append(middlewars, h.AugmentCompat.GetLoginToken)...)
}

func compatRouteMiddlewares(
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	opsService *service.OpsService,
	cfg *config.Config,
) []gin.HandlerFunc {
	maxBodySize := int64(8 << 20)
	if cfg != nil && cfg.Gateway.MaxBodySize > 0 {
		maxBodySize = cfg.Gateway.MaxBodySize
	}
	return []gin.HandlerFunc{
		middleware.RequestBodyLimit(maxBodySize),
		middleware.ClientRequestID(),
		handler.OpsErrorLoggerMiddleware(opsService),
		handler.InboundEndpointMiddleware(),
		gin.HandlerFunc(apiKeyAuth),
	}
}
