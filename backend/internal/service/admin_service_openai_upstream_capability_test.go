//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type openAIUpstreamCapabilityAccountRepoStub struct {
	mockAccountRepoForGemini
	account      *Account
	created      *Account
	createCalls  int
	updateCalls  int
}

func (r *openAIUpstreamCapabilityAccountRepoStub) Create(_ context.Context, account *Account) error {
	r.createCalls++
	r.created = account
	return nil
}

func (r *openAIUpstreamCapabilityAccountRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account != nil {
		return r.account, nil
	}
	return &Account{ID: id}, nil
}

func (r *openAIUpstreamCapabilityAccountRepoStub) Update(_ context.Context, account *Account) error {
	r.updateCalls++
	r.account = account
	return nil
}

func TestCreateAccount_OfficialOpenAIBaseURLClearsUpstreamCapabilityFlags(t *testing.T) {
	repo := &openAIUpstreamCapabilityAccountRepoStub{}
	svc := &adminServiceImpl{accountRepo: repo}

	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                 "Official OpenAI",
		Platform:             PlatformOpenAI,
		Type:                 AccountTypeAPIKey,
		Credentials:          map[string]any{"api_key": "sk-test", "base_url": "https://api.openai.com"},
		Extra:                map[string]any{"openai_upstream_supports_responses": false, "openai_upstream_supports_chat_completions": true, "openai_upstream_supports_messages": true},
		Concurrency:          1,
		Priority:             1,
		SkipDefaultGroupBind: true,
	})

	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, 1, repo.createCalls)
	require.NotNil(t, repo.created)
	require.NotContains(t, repo.created.Extra, "openai_upstream_supports_responses")
	require.NotContains(t, repo.created.Extra, "openai_upstream_supports_chat_completions")
	require.NotContains(t, repo.created.Extra, "openai_upstream_supports_messages")
}

func TestUpdateAccount_OfficialOpenAIBaseURLClearsUpstreamCapabilityFlags(t *testing.T) {
	accountID := int64(88)
	repo := &openAIUpstreamCapabilityAccountRepoStub{
		account: &Account{
			ID:       accountID,
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Status:   StatusActive,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://gateway.example.com/v1",
			},
			Extra: map[string]any{
				"openai_upstream_supports_responses":        false,
				"openai_upstream_supports_chat_completions": true,
				"openai_upstream_supports_messages":         false,
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	updated, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://api.openai.com/v1",
		},
		Extra: map[string]any{
			"openai_upstream_supports_responses":        false,
			"openai_upstream_supports_chat_completions": true,
			"openai_upstream_supports_messages":         true,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, 1, repo.updateCalls)
	require.NotContains(t, repo.account.Extra, "openai_upstream_supports_responses")
	require.NotContains(t, repo.account.Extra, "openai_upstream_supports_chat_completions")
	require.NotContains(t, repo.account.Extra, "openai_upstream_supports_messages")
}
