package setup

import (
	"os"
	"strings"
	"testing"
)

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

func TestGetDataDirPrefersHuggingFaceDataDir(t *testing.T) {
	oldSpaceID := os.Getenv("SPACE_ID")
	oldDataDir := os.Getenv("DATA_DIR")
	defer func() {
		_ = os.Setenv("SPACE_ID", oldSpaceID)
		_ = os.Setenv("DATA_DIR", oldDataDir)
	}()

	_ = os.Setenv("DATA_DIR", "")
	_ = os.Setenv("SPACE_ID", "demo-space")

	if _, err := os.Stat("/data"); err == nil {
		if got := GetDataDir(); got != "/data" {
			t.Fatalf("GetDataDir()=%q, want %q", got, "/data")
		}
	}
}

func TestDataDirCandidatesIncludesHuggingFacePath(t *testing.T) {
	t.Setenv("DATA_DIR", "")
	t.Setenv("SPACE_HOST", "demo.hf.space")

	candidates := dataDirCandidates()
	if len(candidates) < 2 {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
	if candidates[0] != "/data" {
		t.Fatalf("first candidate=%q, want /data", candidates[0])
	}
}

func TestEmbeddedRedisEnabled(t *testing.T) {
	t.Run("enabled when env is true", func(t *testing.T) {
		t.Setenv("EMBEDDED_REDIS_ENABLED", "true")
		if !embeddedRedisEnabled() {
			t.Fatalf("embeddedRedisEnabled() = false, want true")
		}
	})

	t.Run("disabled by default", func(t *testing.T) {
		t.Setenv("EMBEDDED_REDIS_ENABLED", "")
		if embeddedRedisEnabled() {
			t.Fatalf("embeddedRedisEnabled() = true, want false")
		}
	})
}

func TestDefaultRedisHostFromEnv(t *testing.T) {
	t.Run("embedded redis uses loopback ip", func(t *testing.T) {
		t.Setenv("EMBEDDED_REDIS_ENABLED", "true")
		if got := defaultRedisHostFromEnv(); got != "127.0.0.1" {
			t.Fatalf("defaultRedisHostFromEnv()=%q, want %q", got, "127.0.0.1")
		}
	})

	t.Run("non embedded redis uses localhost", func(t *testing.T) {
		t.Setenv("EMBEDDED_REDIS_ENABLED", "false")
		if got := defaultRedisHostFromEnv(); got != "localhost" {
			t.Fatalf("defaultRedisHostFromEnv()=%q, want %q", got, "localhost")
		}
	})
}
