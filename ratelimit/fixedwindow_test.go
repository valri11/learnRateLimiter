package ratelimit

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zapcore"

	mdlogger "github.com/valri11/go-servicepack/logger"
)

func Test_LocalFixedWindow_NoBreach(t *testing.T) {
	limitPerSec := 10
	sw, err := NewLocalFixedWindowLimit(limitPerSec)
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
	assert.True(t, res)

	for i := 1; i < 10; i++ {
		getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
		res = sw.TryPassRequestLimit(ctx)
		assert.True(t, res)
	}

	getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Second) }
	res = sw.TryPassRequestLimit(ctx)
	assert.True(t, res)
}

func Test_LocalFixedWindow_Breach(t *testing.T) {
	limitPerSec := 10
	sw, err := NewLocalFixedWindowLimit(limitPerSec)
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
	assert.True(t, res)

	for i := 1; i < 10; i++ {
		getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
		res = sw.TryPassRequestLimit(ctx)
		assert.True(t, res)
	}

	getTimeNowFn = func() time.Time { return refTime.Add(1 * time.Millisecond) }
	res = sw.TryPassRequestLimit(ctx)
	assert.False(t, res)
}
