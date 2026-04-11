package service

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

const transientDBBackoffWindow = 30 * time.Second

type transientDBBackoff struct {
	mu       sync.Mutex
	until    time.Time
	cooldown time.Duration
}

func newTransientDBBackoff(cooldown time.Duration) *transientDBBackoff {
	if cooldown <= 0 {
		cooldown = transientDBBackoffWindow
	}
	return &transientDBBackoff{cooldown: cooldown}
}

func (b *transientDBBackoff) shouldSkip(now time.Time) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.until.IsZero() && now.Before(b.until)
}

func (b *transientDBBackoff) record(err error, now time.Time) bool {
	if b == nil || !isTransientDBConnectionError(err) {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.until.IsZero() && now.Before(b.until) {
		return false
	}
	b.until = now.Add(b.cooldown)
	return true
}

func (b *transientDBBackoff) reset() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.until = time.Time{}
}

func isTransientDBConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, driver.ErrBadConn) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}

	transientSubstrings := []string{
		"can't assign requested address",
		"cannot assign requested address",
		"connection refused",
		"connection reset by peer",
		"broken pipe",
		"i/o timeout",
		"no route to host",
		"network is unreachable",
		"server closed the connection unexpectedly",
		"sql: connection is already closed",
	}
	for _, needle := range transientSubstrings {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}
