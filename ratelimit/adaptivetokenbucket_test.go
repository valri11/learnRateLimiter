package ratelimit

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	mdlogger "github.com/valri11/go-servicepack/logger"
	"github.com/valri11/learnRateLimiter/config"
	"go.uber.org/zap/zapcore"
)

func Test_LocalAdaptiveTokenBucket_CreateLimiter(t *testing.T) {

	store := config.Store{
		Type: "localAdaptiveTokenBucket",
		Parameters: map[string]string{
			"tiers": "50,20ms,1m,10;100,10ms,1m,10;200,5ms,1m,10",
		},
	}

	rateLimitPerSecUnused := 0
	tb, err := NewLocalAdaptiveTokenBucketLimit(store, rateLimitPerSecUnused)
	assert.NoError(t, err)

	expectedTiers := []RateLimitTier{
		{
			Capacity:           50,
			RefillInterval:     20,
			WindowDuration:     60000,
			NextTierRejectRate: 10,
		},
		{
			Capacity:           100,
			RefillInterval:     10,
			WindowDuration:     60000,
			NextTierRejectRate: 10,
		},
		{
			Capacity:           200,
			RefillInterval:     5,
			WindowDuration:     60000,
			NextTierRejectRate: 10,
		},
	}

	assert.ElementsMatch(t, expectedTiers, tb.Tiers)
}

func Test_LocalAdaptiveTokenBucket_NoBreach(t *testing.T) {
	store := config.Store{
		Type: "localAdaptiveTokenBucket",
		Parameters: map[string]string{
			"tiers": "10,100ms,1m,10;20,50ms,1m,10;",
		},
	}

	rateLimitPerSecUnused := 0
	lm, err := NewLocalAdaptiveTokenBucketLimit(store, rateLimitPerSecUnused)
	assert.NoError(t, err)

	expectedTiers := []RateLimitTier{
		{
			Capacity:           10,
			RefillInterval:     100,
			WindowDuration:     60000,
			NextTierRejectRate: 10,
		},
		{
			Capacity:           20,
			RefillInterval:     50,
			WindowDuration:     60000,
			NextTierRejectRate: 10,
		},
	}

	assert.ElementsMatch(t, expectedTiers, lm.Tiers)

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
	res := lm.TryPassRequestLimit(ctx)
	assert.True(t, res)

	for i := 1; i < 10; i++ {
		getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
		res = lm.TryPassRequestLimit(ctx)
		assert.True(t, res)
	}

	getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Second) }
	res = lm.TryPassRequestLimit(ctx)
	assert.True(t, res)
}

func Test_LocalAdaptiveTokenBucket_Breach(t *testing.T) {
	store := config.Store{
		Type: "localAdaptiveTokenBucket",
		Parameters: map[string]string{
			"tiers": "10,100ms,1m,10;20,50ms,1m,10;",
		},
	}

	rateLimitPerSecUnused := 0
	lm, err := NewLocalAdaptiveTokenBucketLimit(store, rateLimitPerSecUnused)
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
	res := lm.TryPassRequestLimit(ctx)
	assert.True(t, res)

	for i := 1; i < 10; i++ {
		getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
		res = lm.TryPassRequestLimit(ctx)
		assert.True(t, res)
	}

	getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
	res = lm.TryPassRequestLimit(ctx)
	assert.False(t, res)
}

func Test_LocalAdaptiveTokenBucket_BreachAndAdvanceTier(t *testing.T) {
	store := config.Store{
		Type: "localAdaptiveTokenBucket",
		Parameters: map[string]string{
			"tiers": "10,100ms,1m,10;20,50ms,1m,10;",
		},
	}

	rateLimitPerSecUnused := 0
	lm, err := NewLocalAdaptiveTokenBucketLimit(store, rateLimitPerSecUnused)
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
	res := lm.TryPassRequestLimit(ctx)
	assert.True(t, res)

	for i := 1; i < 10; i++ {
		getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
		res = lm.TryPassRequestLimit(ctx)
		assert.True(t, res)
	}

	getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
	res = lm.TryPassRequestLimit(ctx)
	assert.False(t, res)

	// after 60sec - tier window passed
	getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Minute) }
	res = lm.TryPassRequestLimit(ctx)
	assert.True(t, res)
}
