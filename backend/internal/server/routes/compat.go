package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// RegisterCompatRoutes registers thin Cursor/Augment compatible route entrypoints.
func RegisterCompatRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
) {
	registerCursorCompatRoutes(r, h, apiKeyAuth, apiKeyService, subscriptionService, opsService, settingService, cfg)
	registerAugmentCompatRoutes(r, h, apiKeyAuth, apiKeyService, subscriptionService, opsService, settingService, cfg)
}

func registerCursorCompatRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
) {
	if r == nil || h == nil {
		return
	}

	cursorCompat := handler.NewCursorCompatHandler(h.Gateway, h.OpenAIGateway)
	middlewares := compatRouteMiddlewares(apiKeyAuth, opsService, cfg)
	if apiKeyService != nil {
		middlewares = compatRouteMiddlewares(
			middleware.NewAPIKeyAuthMiddlewareWithWriter(apiKeyService, subscriptionService, cfg, middleware.CursorAuthErrorWriter),
			opsService,
			cfg,
		)
	}
	cursor := r.Group("/cursor/v1")
	cursor.Use(middlewares...)
	if settingService != nil {
		cursor.Use(middleware.RequireGroupAssignment(settingService, middleware.CursorErrorWriter))
	}
	{
		cursor.POST("/chat/completions", cursorCompat.ChatCompletions)
		cursor.POST("/responses", cursorCompat.Responses)
		cursor.POST("/messages", cursorCompat.Messages)
		cursor.POST("/messages/count_tokens", cursorCompat.CountTokens)
		cursor.GET("/models", cursorCompat.Models)
	}
}

func registerAugmentCompatRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
) {
	if r == nil || h == nil {
		return
	}

	augmentCompat := handler.NewAugmentCompatHandler(h.Gateway, h.OpenAIGateway, gatewayMaxBodySizeBytes(cfg))
	middlewares := compatRouteMiddlewares(apiKeyAuth, opsService, cfg)
	if apiKeyService != nil {
		middlewares = compatRouteMiddlewares(
			middleware.NewAPIKeyAuthMiddlewareWithWriter(apiKeyService, subscriptionService, cfg, middleware.AugmentAuthErrorWriter),
			opsService,
			cfg,
		)
	}
	if settingService != nil {
		middlewares = append(middlewares, middleware.RequireGroupAssignment(settingService, middleware.AugmentErrorWriter))
	}

	r.POST("/chat-stream", compatRouteHandlers(middlewares, augmentCompat.ChatStream)...)
	r.GET("/usage/api/get-models", compatRouteHandlers(middlewares, augmentCompat.GetModels)...)
	r.GET("/usage/api/balance", compatRouteHandlers(middlewares, augmentCompat.Balance)...)
	r.GET("/usage/api/getLoginToken", compatRouteHandlers(middlewares, augmentCompat.GetLoginToken)...)
}

func compatRouteHandlers(middlewares []gin.HandlerFunc, endpoint gin.HandlerFunc) []gin.HandlerFunc {
	handlers := make([]gin.HandlerFunc, 0, len(middlewares)+1)
	handlers = append(handlers, middlewares...)
	handlers = append(handlers, endpoint)
	return handlers
}

func gatewayMaxBodySizeBytes(cfg *config.Config) int64 {
	maxBodySize := int64(8 << 20)
	if cfg != nil && cfg.Gateway.MaxBodySize > 0 {
		maxBodySize = cfg.Gateway.MaxBodySize
	}
	return maxBodySize
}

func compatRouteMiddlewares(
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	opsService *service.OpsService,
	cfg *config.Config,
) []gin.HandlerFunc {
	maxBodySize := gatewayMaxBodySizeBytes(cfg)
	return []gin.HandlerFunc{
		middleware.RequestBodyLimit(maxBodySize),
		middleware.ClientRequestID(),
		handler.OpsErrorLoggerMiddleware(opsService),
		handler.InboundEndpointMiddleware(),
		gin.HandlerFunc(apiKeyAuth),
	}
}
