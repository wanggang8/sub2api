package setup

import (
	"os"
	"strings"
	"testing"
)

func TestBuildSetupConfigFromEnv_EmbeddedRedisDefaultsHostPort(t *testing.T) {
	t.Setenv("EMBEDDED_REDIS_ENABLED", "true")
	t.Setenv("REDIS_HOST", "")
	t.Setenv("REDIS_PORT", "")

	cfg := buildSetupConfigFromEnv()

	if cfg.Redis.Host != "127.0.0.1" {
		t.Fatalf("Redis.Host=%q, want %q", cfg.Redis.Host, "127.0.0.1")
	}
	if cfg.Redis.Port != 6379 {
		t.Fatalf("Redis.Port=%d, want %d", cfg.Redis.Port, 6379)
	}
}

func TestBuildSetupConfigFromEnv_ExplicitRedisHostWinsInEmbeddedMode(t *testing.T) {
	t.Setenv("EMBEDDED_REDIS_ENABLED", "true")
	t.Setenv("REDIS_HOST", "redis.internal")
	t.Setenv("REDIS_PORT", "6380")

	cfg := buildSetupConfigFromEnv()

	if cfg.Redis.Host != "redis.internal" {
		t.Fatalf("Redis.Host=%q, want %q", cfg.Redis.Host, "redis.internal")
	}
	if cfg.Redis.Port != 6380 {
		t.Fatalf("Redis.Port=%d, want %d", cfg.Redis.Port, 6380)
	}
}

func TestDecideAdminBootstrap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		totalUsers int64
		adminUsers int64
		should     bool
		reason     string
	}{
		{
			name:       "empty database should create admin",
			totalUsers: 0,
			adminUsers: 0,
			should:     true,
			reason:     adminBootstrapReasonEmptyDatabase,
		},
		{
			name:       "admin exists should skip",
			totalUsers: 10,
			adminUsers: 1,
			should:     false,
			reason:     adminBootstrapReasonAdminExists,
		},
		{
			name:       "users exist without admin should skip",
			totalUsers: 5,
			adminUsers: 0,
			should:     false,
			reason:     adminBootstrapReasonUsersExistWithoutAdmin,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decideAdminBootstrap(tc.totalUsers, tc.adminUsers)
			if got.shouldCreate != tc.should {
				t.Fatalf("shouldCreate=%v, want %v", got.shouldCreate, tc.should)
			}
			if got.reason != tc.reason {
				t.Fatalf("reason=%q, want %q", got.reason, tc.reason)
			}
		})
	}
}

func TestSetupDefaultAdminConcurrency(t *testing.T) {
	t.Run("simple mode admin uses higher concurrency", func(t *testing.T) {
		t.Setenv("RUN_MODE", "simple")
		if got := setupDefaultAdminConcurrency(); got != simpleModeAdminConcurrency {
			t.Fatalf("setupDefaultAdminConcurrency()=%d, want %d", got, simpleModeAdminConcurrency)
		}
	})

	t.Run("standard mode keeps existing default", func(t *testing.T) {
		t.Setenv("RUN_MODE", "standard")
		if got := setupDefaultAdminConcurrency(); got != defaultUserConcurrency {
			t.Fatalf("setupDefaultAdminConcurrency()=%d, want %d", got, defaultUserConcurrency)
		}
	})
}

func TestCliValidateUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		want     bool
	}{
		{name: "simple username", username: "postgres", want: true},
		{name: "underscore username", username: "user_name", want: true},
		{name: "supabase pooler username", username: "postgres.uxjftkfxqsafrghfhqrr", want: true},
		{name: "reject spaces", username: "user name", want: false},
		{name: "reject quotes", username: "\"postgres\"", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := cliValidateUsername(tc.username); got != tc.want {
				t.Fatalf("cliValidateUsername(%q)=%v, want %v", tc.username, got, tc.want)
			}
		})
	}
}

func TestWriteConfigFileKeepsDefaultUserConcurrency(t *testing.T) {
	t.Setenv("RUN_MODE", "simple")
	t.Setenv("DATA_DIR", t.TempDir())

	if err := writeConfigFile(&SetupConfig{}); err != nil {
		t.Fatalf("writeConfigFile() error = %v", err)
	}

	data, err := os.ReadFile(GetConfigFilePath())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if !strings.Contains(string(data), "user_concurrency: 5") {
		t.Fatalf("config missing default user concurrency, got:\n%s", string(data))
	}
}
