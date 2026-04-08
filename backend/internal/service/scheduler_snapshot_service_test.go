package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type schedulerSnapshotCacheStub struct {
	watermark               int64
	setWatermarkCtxCanceled bool
	buckets                 []SchedulerBucket
	listBucketsErr          error
	tryLockResult           bool
}

func (s *schedulerSnapshotCacheStub) GetSnapshot(ctx context.Context, bucket SchedulerBucket) ([]*Account, bool, error) {
	return nil, false, nil
}

func (s *schedulerSnapshotCacheStub) SetSnapshot(ctx context.Context, bucket SchedulerBucket, accounts []Account) error {
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

func TestSchedulerSnapshotServiceRunInitialRebuildBootstrapsOutboxWatermarkWhenMissing(t *testing.T) {
	cache := &schedulerSnapshotCacheStub{
		buckets: []SchedulerBucket{
			{GroupID: 0, Platform: PlatformOpenAI, Mode: SchedulerModeSingle},
		},
	}
	outbox := &schedulerOutboxRepoStub{maxID: 99}
	svc := &SchedulerSnapshotService{
		cache:      cache,
		outboxRepo: outbox,
	}

	svc.runInitialRebuild()

	require.Equal(t, int64(99), cache.watermark)
}

func TestSchedulerSnapshotServiceBootstrapOutboxWatermarkDoesNotOverrideExisting(t *testing.T) {
	cache := &schedulerSnapshotCacheStub{watermark: 12}
	outbox := &schedulerOutboxRepoStub{maxID: 99}
	svc := &SchedulerSnapshotService{
		cache:      cache,
		outboxRepo: outbox,
	}

	svc.bootstrapOutboxWatermark(context.Background())

	require.Equal(t, int64(12), cache.watermark)
}
