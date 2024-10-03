package ratelimit

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	mdlogger "github.com/valri11/go-servicepack/logger"
	"github.com/valri11/learnRateLimiter/config"
	"go.uber.org/zap"
)

type LocalFixedWindowLimit struct {
	Timestamp int64
	Limit     int64
	Counter   int64
	mx        sync.Mutex
}

func NewLocalFixedWindowLimit(store config.Store) (*LocalFixedWindowLimit, error) {
	rateLimitPerSec, err := strconv.Atoi(store.Parameters["ratelimitpersec"])
	if err != nil {
		return nil, err
	}

	st := LocalFixedWindowLimit{
		Timestamp: time.Now().Unix(),
		Limit:     int64(rateLimitPerSec),
	}
	return &st, nil
}

func (st *LocalFixedWindowLimit) TryPassRequestLimit(ctx context.Context) RequestLimitAllowance {
	st.mx.Lock()
	defer st.mx.Unlock()

	res := RequestLimitAllowance{
		Allowed:        false,
		Limit:          st.Limit,
		LimitWindowSec: 1,
		Remaining:      0,
	}

	tsNowSeconds := getTimeNowFn().Unix()

	if tsNowSeconds == st.Timestamp {
		if st.Counter >= st.Limit {
			return res
		}
	} else {
		st.Timestamp = tsNowSeconds
		st.Counter = 0
	}

	st.Counter++

	res.Allowed = true
	res.Remaining = res.Limit - st.Counter

	return res
}

type RedisFixedWindowLimit struct {
	store        config.Store
	Timestamp    int64
	Limit        int64
	Counter      int64
	client       redis.UniversalClient
	limitKeyName string
}

func NewRedisFixedWindowLimit(store config.Store) (*RedisFixedWindowLimit, error) {
	rateLimitPerSec, err := strconv.Atoi(store.Parameters["ratelimitpersec"])
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs: []string{store.Parameters["connection"]},
	})

	// Enable tracing instrumentation.
	if err := redisotel.InstrumentTracing(client); err != nil {
		return nil, err
	}

	// Enable metrics instrumentation.
	if err := redisotel.InstrumentMetrics(client); err != nil {
		return nil, err
	}

	err = client.Ping(ctx).Err()
	if err != nil {
		return nil, err
	}

	st := RedisFixedWindowLimit{
		store:        store,
		Timestamp:    time.Now().Unix(),
		Limit:        int64(rateLimitPerSec),
		client:       client,
		limitKeyName: "fixedWindowLimit",
	}

	return &st, nil
}

func (st *RedisFixedWindowLimit) TryPassRequestLimit(ctx context.Context) RequestLimitAllowance {
	logger := mdlogger.FromContext(ctx)

	res := RequestLimitAllowance{
		Allowed:        false,
		Limit:          st.Limit,
		LimitWindowSec: 1,
		Remaining:      0,
	}

	script := `local current
current = redis.call("incr",KEYS[1])
if current == 1 then
    redis.call("expire",KEYS[1],1)
end
return current
`

	counter, err := st.client.Eval(ctx, script, []string{st.limitKeyName}).Int64()
	if err != nil {
		logger.With(zap.Error(err)).Error("request rate limited, check error message")
		return res
	}

	if counter > st.Limit {
		logger.
			With(zap.Int64("req_count", counter)).
			With(zap.Int64("limit", st.Limit)).
			Warn("breach rate limit")
		return res
	}

	logger.
		With(zap.Int64("req_count", counter)).
		With(zap.Int64("limit", st.Limit)).
		Debug("rate limit check")

	res.Allowed = true
	res.Remaining = res.Limit - counter

	return res
}
