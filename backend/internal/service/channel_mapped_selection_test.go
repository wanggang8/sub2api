package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type channelMappedSelectionAccountRepo struct {
	stubOpenAIAccountRepo
}

func (r channelMappedSelectionAccountRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]Account, error) {
	var result []Account
	for _, acc := range r.accounts {
		if slices.Contains(platforms, acc.Platform) {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (r channelMappedSelectionAccountRepo) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	return r.ListSchedulableByGroupIDAndPlatforms(ctx, 0, platforms)
}

func TestChannelMappedSelectionModelAllowsMappedAccountSupport(t *testing.T) {
	svc := &GatewayService{}
	account := &Account{
		ID:          1,
		Platform:    PlatformAnthropic,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-sonnet-4": "claude-sonnet-4",
			},
		},
	}

	require.False(t, svc.isModelSupportedByAccountWithContext(context.Background(), account, "CustomModel"))

	ctx := WithChannelMappedSelectionModel(context.Background(), "CustomModel", "claude-sonnet-4")
	require.True(t, svc.isModelSupportedByAccountWithContext(ctx, account, "CustomModel"))
}

func TestChannelMappedSelectionModelPreservesAccountAliasSupport(t *testing.T) {
	svc := &GatewayService{}
	account := &Account{
		ID:          1,
		Platform:    PlatformAnthropic,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"CustomModel": "claude-sonnet-4",
			},
		},
	}

	ctx := WithChannelMappedSelectionModel(context.Background(), "CustomModel", "claude-haiku-4")
	require.True(t, svc.isModelSupportedByAccountWithContext(ctx, account, "CustomModel"))
}

func TestChannelMappedSelectionModelUsesMappedModelForRateLimit(t *testing.T) {
	resetAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	account := &Account{
		ID:          1,
		Platform:    PlatformAnthropic,
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"model_rate_limits": map[string]any{
				"claude-sonnet-4": map[string]any{
					"rate_limit_reset_at": resetAt,
				},
			},
		},
	}
	svc := &GatewayService{}

	require.True(t, svc.isAccountSchedulableForModelSelection(context.Background(), account, "CustomModel"))

	ctx := WithChannelMappedSelectionModel(context.Background(), "CustomModel", "claude-sonnet-4")
	require.False(t, svc.isAccountSchedulableForModelSelection(ctx, account, "CustomModel"))
}

func TestSelectAccountWithLoadAwarenessAllowsChannelMappedModelAlias(t *testing.T) {
	repo := channelMappedSelectionAccountRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{
		accounts: []Account{
			{
				ID:          1,
				Platform:    PlatformAnthropic,
				Status:      StatusActive,
				Schedulable: true,
				Concurrency: 5,
				AccountGroups: []AccountGroup{
					{GroupID: 10},
				},
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"claude-sonnet-4": "claude-sonnet-4",
					},
				},
			},
		},
	}}

	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &GatewayService{
		accountRepo: repo,
		cache:       &stubGatewayCache{},
		cfg:         cfg,
	}

	groupID := int64(10)
	baseCtx := context.WithValue(context.Background(), ctxkey.Group, &Group{
		ID:       groupID,
		Platform: PlatformAnthropic,
		Status:   StatusActive,
		Hydrated: true,
	})
	_, err := svc.SelectAccountWithLoadAwareness(baseCtx, &groupID, "", "CustomModel", nil, "", 0)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)

	ctx := WithChannelMappedSelectionModel(baseCtx, "CustomModel", "claude-sonnet-4")
	selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "CustomModel", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(1), selection.Account.ID)
}
