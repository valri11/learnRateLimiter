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

func Test_RedisSlidingWindow_NoBreach(t *testing.T) {
	limitPerSec := 10

	s := miniredis.RunT(t)

	store := config.Store{
		Connection: s.Addr(),
	}

	sw, err := NewRedisSlidingWindowLimit(store, int32(limitPerSec))
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
	assert.True(t, res)

	for i := 1; i < 10; i++ {
		refTime = refTime.Add(1 * time.Millisecond)
		s.SetTime(refTime)
		s.FastForward(1 * time.Millisecond)

		res = sw.TryPassRequestLimit(ctx)
		assert.True(t, res)
	}

	refTime = refTime.Add(1 * time.Second)
	s.SetTime(refTime)
	s.FastForward(1 * time.Second)

	res = sw.TryPassRequestLimit(ctx)
	assert.True(t, res)
}

func Test_RedisSlidingWindow_Breach(t *testing.T) {
	limitPerSec := 10

	s := miniredis.RunT(t)

	store := config.Store{
		Connection: s.Addr(),
	}

	sw, err := NewRedisSlidingWindowLimit(store, int32(limitPerSec))
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
	assert.True(t, res)

	for i := 1; i < 10; i++ {
		refTime = refTime.Add(1 * time.Millisecond)
		s.SetTime(refTime)
		s.FastForward(1 * time.Millisecond)

		res = sw.TryPassRequestLimit(ctx)
		assert.True(t, res)
	}

	refTime = refTime.Add(1 * time.Millisecond)
	s.SetTime(refTime)
	s.FastForward(1 * time.Millisecond)

	res = sw.TryPassRequestLimit(ctx)
	assert.False(t, res)
}
