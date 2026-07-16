package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRepository_PatchCredentialsUsesAtomicJSONBDelta(t *testing.T) {
	exec := &recordingSQLExecutor{result: rowsAffectedResult(1)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)

	err := repo.PatchCredentials(
		context.Background(),
		42,
		map[string]any{
			"access_token":   "new-token",
			"_token_version": int64(123),
		},
		[]string{"id_token", "expires_in"},
	)

	require.NoError(t, err)
	require.Len(t, exec.execQueries, 1)
	normalized := normalizeSQLWhitespace(exec.execQueries[0])
	require.Contains(t, normalized, "COALESCE(credentials, '{}'::jsonb) - COALESCE($2::text[], ARRAY[]::text[])")
	require.Contains(t, normalized, ") || $3::jsonb")
	require.Contains(t, normalized, "WHERE id = $1 AND deleted_at IS NULL")
	require.Len(t, exec.execArgs[0], 3)
	require.Equal(t, int64(42), exec.execArgs[0][0])

	removeArg, ok := exec.execArgs[0][1].(driver.Valuer)
	require.True(t, ok)
	removeValue, err := removeArg.Value()
	require.NoError(t, err)
	require.Equal(t, "{\"id_token\",\"expires_in\"}", removeValue)

	setPayload, ok := exec.execArgs[0][2].([]byte)
	require.True(t, ok)
	var setFields map[string]any
	require.NoError(t, json.Unmarshal(setPayload, &setFields))
	require.Equal(t, "new-token", setFields["access_token"])
	require.Equal(t, float64(123), setFields["_token_version"])
}

func TestAccountRepository_PatchCredentialsReturnsNotFound(t *testing.T) {
	exec := &recordingSQLExecutor{result: rowsAffectedResult(0)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)

	err := repo.PatchCredentials(context.Background(), 404, map[string]any{"access_token": "new"}, nil)

	require.ErrorIs(t, err, service.ErrAccountNotFound)
	removeArg, ok := exec.execArgs[0][1].(driver.Valuer)
	require.True(t, ok)
	removeValue, valueErr := removeArg.Value()
	require.NoError(t, valueErr)
	require.Nil(t, removeValue)
}
