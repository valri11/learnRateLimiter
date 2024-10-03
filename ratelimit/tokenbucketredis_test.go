package ratelimit

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	mdlogger "github.com/valri11/go-servicepack/logger"
	"github.com/valri11/learnRateLimiter/config"
	"go.uber.org/zap/zapcore"
)

func Test_RedisTokenBucketWindow_NoBreach(t *testing.T) {
	s := miniredis.RunT(t)

	store := config.Store{
		Parameters: map[string]string{
			"connection":      s.Addr(),
			"ratelimitpersec": "10",
		},
	}

	sw, err := NewRedisTokenBucketLimit(store)
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
	s.SetTime(refTime)

	res := sw.TryPassRequestLimit(ctx)
	assert.True(t, res.Allowed)

	for i := 1; i < 10; i++ {
		refTime = refTime.Add(1 * time.Millisecond)
		s.SetTime(refTime)
		s.FastForward(1 * time.Millisecond)

		res = sw.TryPassRequestLimit(ctx)
		assert.True(t, res.Allowed)
	}

	refTime = refTime.Add(1 * time.Second)
	s.SetTime(refTime)
	s.FastForward(1 * time.Second)

	res = sw.TryPassRequestLimit(ctx)
	assert.True(t, res.Allowed)
}

func Test_RedisTokenBucket_Breach(t *testing.T) {

	s := miniredis.RunT(t)

	store := config.Store{
		Parameters: map[string]string{
			"connection":      s.Addr(),
			"ratelimitpersec": "10",
		},
	}

	sw, err := NewRedisTokenBucketLimit(store)
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
	s.SetTime(refTime)

	res := sw.TryPassRequestLimit(ctx)
	assert.True(t, res.Allowed)

	for i := 1; i < 10; i++ {
		refTime = refTime.Add(1 * time.Millisecond)
		s.SetTime(refTime)
		s.FastForward(1 * time.Millisecond)

		res = sw.TryPassRequestLimit(ctx)
		assert.True(t, res.Allowed)
	}

	refTime = refTime.Add(1 * time.Millisecond)
	s.SetTime(refTime)
	s.FastForward(1 * time.Millisecond)

	res = sw.TryPassRequestLimit(ctx)
	assert.False(t, res.Allowed)
}

func Test_RedisTokenBucket_NoBreachAfterRefill(t *testing.T) {
	s := miniredis.RunT(t)

	store := config.Store{
		Parameters: map[string]string{
			"connection":      s.Addr(),
			"ratelimitpersec": "10",
		},
	}

	sw, err := NewRedisTokenBucketLimit(store)
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
	s.SetTime(refTime)

	res := sw.TryPassRequestLimit(ctx)
	assert.True(t, res.Allowed)

	for i := 1; i < 10; i++ {
		refTime = refTime.Add(1 * time.Millisecond)
		s.SetTime(refTime)
		s.FastForward(1 * time.Millisecond)

		res = sw.TryPassRequestLimit(ctx)
		assert.True(t, res.Allowed)
	}

	refTime = refTime.Add(100 * time.Millisecond)
	s.SetTime(refTime)
	s.FastForward(100 * time.Millisecond)

	res = sw.TryPassRequestLimit(ctx)
	assert.True(t, res.Allowed)
}
