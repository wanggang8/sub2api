package service

import (
	"context"
	"strings"
)

type channelMappedSelectionContextKey struct{}

type channelMappedSelectionModel struct {
	requested string
	mapped    string
}

// WithChannelMappedSelectionModel records the channel-level model alias resolved
// before account selection. Account filtering can then accept either the client
// alias or the channel target while model-scoped limits use the target model.
func WithChannelMappedSelectionModel(ctx context.Context, requestedModel, mappedModel string) context.Context {
	requestedModel = strings.TrimSpace(requestedModel)
	mappedModel = strings.TrimSpace(mappedModel)
	if requestedModel == "" || mappedModel == "" || requestedModel == mappedModel {
		return ctx
	}
	return context.WithValue(ctx, channelMappedSelectionContextKey{}, channelMappedSelectionModel{
		requested: requestedModel,
		mapped:    mappedModel,
	})
}

func channelMappedSelectionModelFromContext(ctx context.Context, requestedModel string) (string, bool) {
	if ctx == nil {
		return "", false
	}
	model, ok := ctx.Value(channelMappedSelectionContextKey{}).(channelMappedSelectionModel)
	if !ok {
		return "", false
	}
	if strings.TrimSpace(requestedModel) != model.requested {
		return "", false
	}
	return model.mapped, true
}

func modelForChannelMappedSelection(ctx context.Context, requestedModel string) string {
	if mappedModel, ok := channelMappedSelectionModelFromContext(ctx, requestedModel); ok {
		return mappedModel
	}
	return requestedModel
}
