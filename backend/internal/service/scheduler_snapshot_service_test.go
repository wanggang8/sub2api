package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type schedulerSnapshotCacheStub struct {
	watermark               int64
	setWatermarkCtxCanceled bool
	buckets                 []SchedulerBucket
	listBucketsErr          error
	tryLockResult           bool
	setSnapshotHook         func()
}

func (s *schedulerSnapshotCacheStub) GetSnapshot(ctx context.Context, bucket SchedulerBucket) ([]*Account, bool, error) {
	return nil, false, nil
}

func (s *schedulerSnapshotCacheStub) SetSnapshot(ctx context.Context, bucket SchedulerBucket, accounts []Account) error {
	if s.setSnapshotHook != nil {
		s.setSnapshotHook()
	}
	return nil
}

func (s *schedulerSnapshotCacheStub) GetAccount(ctx context.Context, accountID int64) (*Account, error) {
	return nil, nil
}

func (s *schedulerSnapshotCacheStub) SetAccount(ctx context.Context, account *Account) error {
	return nil
}

func (s *schedulerSnapshotCacheStub) DeleteAccount(ctx context.Context, accountID int64) error {
	return nil
}

func (s *schedulerSnapshotCacheStub) UpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	return nil
}

func (s *schedulerSnapshotCacheStub) TryLockBucket(ctx context.Context, bucket SchedulerBucket, ttl time.Duration) (bool, error) {
	return s.tryLockResult, nil
}

func (s *schedulerSnapshotCacheStub) ListBuckets(ctx context.Context) ([]SchedulerBucket, error) {
	if s.listBucketsErr != nil {
		return nil, s.listBucketsErr
	}
	return s.buckets, nil
}

func (s *schedulerSnapshotCacheStub) GetOutboxWatermark(ctx context.Context) (int64, error) {
	return s.watermark, nil
}

func (s *schedulerSnapshotCacheStub) SetOutboxWatermark(ctx context.Context, id int64) error {
	s.setWatermarkCtxCanceled = ctx.Err() != nil
	s.watermark = id
	return nil
}

type schedulerOutboxRepoStub struct {
	listAfterCalls int
	listAfterErr   error
	maxID          int64
	maxIDErr       error
}

func (s *schedulerOutboxRepoStub) ListAfter(ctx context.Context, afterID int64, limit int) ([]SchedulerOutboxEvent, error) {
	s.listAfterCalls++
	if s.listAfterErr != nil {
		return nil, s.listAfterErr
	}
	return nil, nil
}

func (s *schedulerOutboxRepoStub) MaxID(ctx context.Context) (int64, error) {
	if s.maxIDErr != nil {
		return 0, s.maxIDErr
	}
	return s.maxID, nil
}

type schedulerAccountRepoStub struct{}

func (s *schedulerAccountRepoStub) Create(ctx context.Context, account *Account) error {
	return nil
}
func (s *schedulerAccountRepoStub) GetByID(ctx context.Context, id int64) (*Account, error) {
	return nil, ErrAccountNotFound
}
func (s *schedulerAccountRepoStub) GetByIDs(ctx context.Context, ids []int64) ([]*Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ExistsByID(ctx context.Context, id int64) (bool, error) {
	return false, nil
}
func (s *schedulerAccountRepoStub) GetByCRSAccountID(ctx context.Context, crsAccountID string) (*Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) FindByExtraField(ctx context.Context, key string, value any) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ListCRSAccountIDs(ctx context.Context) (map[string]int64, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) Update(ctx context.Context, account *Account) error {
	return nil
}
func (s *schedulerAccountRepoStub) Delete(ctx context.Context, id int64) error { return nil }
func (s *schedulerAccountRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]Account, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (s *schedulerAccountRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, accountType, status, search string, groupID int64, privacyMode string) ([]Account, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (s *schedulerAccountRepoStub) ListByGroup(ctx context.Context, groupID int64) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ListActive(ctx context.Context) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ListByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) UpdateLastUsed(ctx context.Context, id int64) error { return nil }
func (s *schedulerAccountRepoStub) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	return nil
}
func (s *schedulerAccountRepoStub) SetError(ctx context.Context, id int64, errorMsg string) error {
	return nil
}
func (s *schedulerAccountRepoStub) ClearError(ctx context.Context, id int64) error { return nil }
func (s *schedulerAccountRepoStub) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	return nil
}
func (s *schedulerAccountRepoStub) AutoPauseExpiredAccounts(ctx context.Context, now time.Time) (int64, error) {
	return 0, nil
}
func (s *schedulerAccountRepoStub) BindGroups(ctx context.Context, accountID int64, groupIDs []int64) error {
	return nil
}
func (s *schedulerAccountRepoStub) ListSchedulable(ctx context.Context) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ListSchedulableByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]Account, error) {
	return nil, nil
}
func (s *schedulerAccountRepoStub) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	return nil
}
func (s *schedulerAccountRepoStub) SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time) error {
	return nil
}
func (s *schedulerAccountRepoStub) SetOverloaded(ctx context.Context, id int64, until time.Time) error {
	return nil
}
func (s *schedulerAccountRepoStub) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	return nil
}
func (s *schedulerAccountRepoStub) ClearTempUnschedulable(ctx context.Context, id int64) error {
	return nil
}
func (s *schedulerAccountRepoStub) ClearRateLimit(ctx context.Context, id int64) error { return nil }
func (s *schedulerAccountRepoStub) ClearAntigravityQuotaScopes(ctx context.Context, id int64) error {
	return nil
}
func (s *schedulerAccountRepoStub) ClearModelRateLimits(ctx context.Context, id int64) error {
	return nil
}
func (s *schedulerAccountRepoStub) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	return nil
}
func (s *schedulerAccountRepoStub) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	return nil
}
func (s *schedulerAccountRepoStub) BulkUpdate(ctx context.Context, ids []int64, updates AccountBulkUpdate) (int64, error) {
	return 0, nil
}
func (s *schedulerAccountRepoStub) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) error {
	return nil
}
func (s *schedulerAccountRepoStub) ResetQuotaUsed(ctx context.Context, id int64) error { return nil }

func TestSchedulerSnapshotServicePollOutboxTransientDBErrorBacksOff(t *testing.T) {
	cache := &schedulerSnapshotCacheStub{}
	outbox := &schedulerOutboxRepoStub{
		listAfterErr: errors.New("read tcp 198.18.0.1:52243->198.18.0.23:5432: read: can't assign requested address"),
	}
	svc := &SchedulerSnapshotService{
		cache:      cache,
		outboxRepo: outbox,
		stopCh:     make(chan struct{}),
	}

	svc.pollOutbox()
	svc.pollOutbox()

	require.Equal(t, 1, outbox.listAfterCalls)
}

func TestSchedulerSnapshotServiceWriteOutboxWatermarkUsesFreshContext(t *testing.T) {
	cache := &schedulerSnapshotCacheStub{}
	svc := &SchedulerSnapshotService{
		cache: cache,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.writeOutboxWatermark(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, int64(42), cache.watermark)
	require.False(t, cache.setWatermarkCtxCanceled)
}

func TestSchedulerSnapshotServiceRunInitialRebuildBootstrapsCapturedOutboxWatermarkWhenMissing(t *testing.T) {
	cache := &schedulerSnapshotCacheStub{
		buckets: []SchedulerBucket{
			{GroupID: 0, Platform: PlatformOpenAI, Mode: SchedulerModeSingle},
		},
		tryLockResult: true,
	}
	outbox := &schedulerOutboxRepoStub{maxID: 99}
	cache.setSnapshotHook = func() {
		outbox.maxID = 100
	}
	svc := &SchedulerSnapshotService{
		cache:       cache,
		outboxRepo:  outbox,
		accountRepo: &schedulerAccountRepoStub{},
	}

	svc.runInitialRebuild()

	require.Equal(t, int64(99), cache.watermark)
}

func TestSchedulerSnapshotServiceRunInitialRebuildDoesNotOverrideWatermarkAdvancedDuringRebuild(t *testing.T) {
	cache := &schedulerSnapshotCacheStub{
		buckets: []SchedulerBucket{
			{GroupID: 0, Platform: PlatformOpenAI, Mode: SchedulerModeSingle},
		},
		tryLockResult: true,
	}
	outbox := &schedulerOutboxRepoStub{maxID: 99}
	cache.setSnapshotHook = func() {
		cache.watermark = 120
	}
	svc := &SchedulerSnapshotService{
		cache:       cache,
		outboxRepo:  outbox,
		accountRepo: &schedulerAccountRepoStub{},
	}

	svc.runInitialRebuild()

	require.Equal(t, int64(120), cache.watermark)
}
