package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestIsTLSInsecureSkipVerifyEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		account  Account
		expected bool
	}{
		{
			name:     "nil extra defaults false",
			account:  Account{},
			expected: false,
		},
		{
			name: "missing flag defaults false",
			account: Account{
				Extra: map[string]any{},
			},
			expected: false,
		},
		{
			name: "explicit false remains false",
			account: Account{
				Extra: map[string]any{"tls_insecure_skip_verify": false},
			},
			expected: false,
		},
		{
			name: "explicit true enables skip verify",
			account: Account{
				Extra: map[string]any{"tls_insecure_skip_verify": true},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, tt.account.IsTLSInsecureSkipVerifyEnabled())
		})
	}
}

func TestUpstreamTLSOptionsFromAccount(t *testing.T) {
	t.Parallel()

	profile := &tlsfingerprint.Profile{Name: "test"}
	account := &Account{
		Extra: map[string]any{"tls_insecure_skip_verify": true},
	}

	opts := UpstreamTLSOptionsFromAccount(account, profile)
	require.NotNil(t, opts)
	require.True(t, opts.InsecureSkipVerify)
	require.Same(t, profile, opts.FingerprintProfile)

	opts = UpstreamTLSOptionsFromAccount(&Account{}, nil)
	require.Nil(t, opts)
}
