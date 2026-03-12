package ratelimit

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	mdlogger "github.com/valri11/go-servicepack/logger"
	"go.uber.org/zap/zapcore"
)

func Test_LocalTokenBucketWindow_NoBreach(t *testing.T) {
	limitPerSec := 10
	sw, err := NewLocalTokenBucketLimit(int32(limitPerSec))
	assert.NoError(t, err)

	logger, err := mdlogger.New(zapcore.DebugLevel, true)
	if err != nil {
		log.Fatalf("ERR: %v", err)
		return
	}
	defer logger.Sync()

	ctx := context.Background()
	ctx = mdlogger.NewContext(ctx, logger)

	refTime := time.Date(1974, time.May, 19, 1, 2, 3, 4, time.UTC)
	getTimeNowFn = func() time.Time { return refTime }
	res := sw.TryPassRequestLimit(ctx)
	assert.True(t, res.Allowed)
	assert.Equal(t, int64(10), res.Limit)

	for i := 1; i < 9; i++ {
		getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
		res = sw.TryPassRequestLimit(ctx)
		assert.True(t, res.Allowed)
	}

	// after 1 second, bucket refills
	getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Second) }
	res = sw.TryPassRequestLimit(ctx)
	assert.True(t, res.Allowed)
}

func Test_LocalTokenBucket_Breach(t *testing.T) {
	limitPerSec := 10
	sw, err := NewLocalTokenBucketLimit(int32(limitPerSec))
	assert.NoError(t, err)

	logger, err := mdlogger.New(zapcore.DebugLevel, true)
	if err != nil {
		log.Fatalf("ERR: %v", err)
		return
	}
	defer logger.Sync()

	ctx := context.Background()
	ctx = mdlogger.NewContext(ctx, logger)

	refTime := time.Date(1974, time.May, 19, 1, 2, 3, 4, time.UTC)
	getTimeNowFn = func() time.Time { return refTime }
	res := sw.TryPassRequestLimit(ctx)
	assert.True(t, res.Allowed)

	for i := 1; i < 9; i++ {
		getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
		res = sw.TryPassRequestLimit(ctx)
		assert.True(t, res.Allowed)
	}

	// 10th request within same millisecond should use the last token
	getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
	res = sw.TryPassRequestLimit(ctx)
	assert.True(t, res.Allowed)
	assert.Equal(t, int64(0), res.Remaining)

	// 11th request should breach
	getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
	res = sw.TryPassRequestLimit(ctx)
	assert.False(t, res.Allowed)
}
