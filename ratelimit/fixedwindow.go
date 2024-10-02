package ratelimit

import (
	"context"
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
	Limit     int32
	Counter   int32
	mx        sync.Mutex
}

func NewLocalFixedWindowLimit(rateLimitPerSec int) (*LocalFixedWindowLimit, error) {
	st := LocalFixedWindowLimit{
		Timestamp: time.Now().Unix(),
		Limit:     int32(rateLimitPerSec),
	}
	return &st, nil
}

func (st *LocalFixedWindowLimit) TryPassRequestLimit(ctx context.Context) bool {
	st.mx.Lock()
	defer st.mx.Unlock()

	tsNowSeconds := getTimeNowFn().Unix()

	if tsNowSeconds == st.Timestamp {
		if st.Counter >= st.Limit {
			return false
		}
	} else {
		st.Timestamp = tsNowSeconds
		st.Counter = 0
	}

	st.Counter++

	return true
}

type RedisFixedWindowLimit struct {
	store        config.Store
	Timestamp    int64
	Limit        int32
	Counter      int32
	client       redis.UniversalClient
	limitKeyName string
}

func NewRedisFixedWindowLimit(store config.Store, rateLimitPerSec int) (*RedisFixedWindowLimit, error) {
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

	err := client.Ping(ctx).Err()
	if err != nil {
		return nil, err
	}

	st := RedisFixedWindowLimit{
		store:        store,
		Timestamp:    time.Now().Unix(),
		Limit:        int32(rateLimitPerSec),
		client:       client,
		limitKeyName: "fixedWindowLimit",
	}

	return &st, nil
}

func (st *RedisFixedWindowLimit) TryPassRequestLimit(ctx context.Context) bool {
	logger := mdlogger.FromContext(ctx)

	script := `local current
current = redis.call("incr",KEYS[1])
if current == 1 then
    redis.call("expire",KEYS[1],1)
end
return current
`

	val, err := st.client.Eval(ctx, script, []string{st.limitKeyName}).Int()
	if err != nil {
		logger.With(zap.Error(err)).Error("request rate limited, check error message")
		return false
	}

	if val > int(st.Limit) {
		logger.
			With(zap.Int("req_count", val)).
			With(zap.Int32("limit", st.Limit)).
			Warn("breach rate limit")
		return false
	}

	logger.
		With(zap.Int("req_count", val)).
		With(zap.Int32("limit", st.Limit)).
		Debug("rate limit check")

	return true

	/*
		pipe := st.client.TxPipeline()

		incr := pipe.Incr(ctx, st.limitKeyName)
		//pipe.Expire(ctx, st.limitKeyName, 1*time.Second)
		pipe.ExpireNX(ctx, st.limitKeyName, 1*time.Second)

		_, err := pipe.Exec(ctx)
		if err != nil {
			logger.With(zap.Error(err)).Error("request rate limited, check error message")
			return false
		}

		if incr.Val() > int64(st.limit.Limit) {
			logger.
				With(zap.Int64("req_count", incr.Val())).
				With(zap.Int32("limit", st.limit.Limit)).
				Warn("breach rate limit")
			return false
		}

		return true
	*/
}
