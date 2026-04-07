package service

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTransientDBBackoffSuppressesRepeatedTransientErrors(t *testing.T) {
	backoff := newTransientDBBackoff(time.Minute)
	err := errors.New("read tcp 198.18.0.1:52665->198.18.0.23:5432: read: can't assign requested address")
	base := time.Date(2026, 4, 7, 20, 15, 0, 0, time.UTC)

	require.True(t, backoff.record(err, base))
	require.True(t, backoff.shouldSkip(base.Add(5*time.Second)))
	require.False(t, backoff.record(err, base.Add(5*time.Second)))
	require.True(t, backoff.record(err, base.Add(61*time.Second)))
}

func TestIsTransientDBConnectionError(t *testing.T) {
	require.True(t, isTransientDBConnectionError(sql.ErrConnDone))
	require.True(t, isTransientDBConnectionError(driver.ErrBadConn))
	require.True(t, isTransientDBConnectionError(errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")))
	require.False(t, isTransientDBConnectionError(errors.New("pq: duplicate key value violates unique constraint")))
}
