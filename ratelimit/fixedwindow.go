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

type FixedWindowLimit struct {
	Timestamp int64
	Limit     int32
	Counter   int32
}

type LocalLimitStore struct {
	store config.Store
	limit FixedWindowLimit
	mx    sync.Mutex
}

func NewLocalLimitStore(store config.Store, rateLimitPerSec int32) (*LocalLimitStore, error) {
	st := LocalLimitStore{
		store: store,
		limit: FixedWindowLimit{
			Timestamp: time.Now().Unix(),
			Limit:     rateLimitPerSec,
		},
	}
	return &st, nil
}

func (st *LocalLimitStore) TryPassRequestLimit(ctx context.Context) bool {
	st.mx.Lock()
	defer st.mx.Unlock()

	tsNowSeconds := time.Now().Unix()

	if tsNowSeconds == st.limit.Timestamp {
		if st.limit.Counter > st.limit.Limit {
			return false
		}
	} else {
		st.limit.Timestamp = time.Now().Unix()
		st.limit.Counter = 0
	}

	st.limit.Counter++

	return true
}

type RedisLimitStore struct {
	store        config.Store
	limit        FixedWindowLimit
	client       redis.UniversalClient
	limitKeyName string
}

func NewRedisLimitStore(store config.Store, rateLimitPerSec int32) (*RedisLimitStore, error) {
	ctx := context.Background()
	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs: []string{store.Connection},
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

	st := RedisLimitStore{
		store: store,
		limit: FixedWindowLimit{
			Timestamp: time.Now().Unix(),
			Limit:     rateLimitPerSec,
		},
		client:       client,
		limitKeyName: "fixedWindowLimit",
	}

	return &st, nil
}

func (st *RedisLimitStore) TryPassRequestLimit(ctx context.Context) bool {
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

	if val > int(st.limit.Limit) {
		logger.
			With(zap.Int("req_count", val)).
			With(zap.Int32("limit", st.limit.Limit)).
			Warn("breach rate limit")
		return false
	}

	logger.
		With(zap.Int("req_count", val)).
		With(zap.Int32("limit", st.limit.Limit)).
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
