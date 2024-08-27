package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	mdlogger "github.com/valri11/go-servicepack/logger"
	"github.com/valri11/learnRateLimiter/config"
)

type FixedWindowLimit struct {
	Timestamp int64
	Limit     int32
	Counter   int32
}

type LimitStore interface {
	//GetRateLimit() FixedWindowLimit
	//ResetRateLimit()

	TryPassRequestLimit(ctx context.Context) bool
}

func NewLimitStore(store config.Store, rateLimitPerSec int32) (LimitStore, error) {
	if store.Type == "local" {
		return NewLocalLimitStore(store, rateLimitPerSec)
	} else if store.Type == "redis" {
		return NewRedisLimitStore(store, rateLimitPerSec)
	}

	return nil, errors.New(fmt.Sprintf("unknown store type: %s", store.Type))
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
		if st.limit.Counter >= st.limit.Limit {
			return false
		}
	} else {
		st.limit.Timestamp = time.Now().Unix()
		st.limit.Counter = 0
	}

	st.limit.Counter++

	return true
}

func (st *LocalLimitStore) GetRateLimit() FixedWindowLimit {
	return st.limit
}

func (st *LocalLimitStore) ResetRateLimit() {
	st.mx.Lock()
	defer st.mx.Unlock()

	st.limit.Timestamp = time.Now().Unix()
	st.limit.Counter = 0
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
		limitKeyName: "learnRateLimiter",
	}

	return &st, nil
}

func (st *RedisLimitStore) TryPassRequestLimit(ctx context.Context) bool {

	logger := mdlogger.FromContext(ctx)
	val, err := st.client.Get(ctx, st.limitKeyName).Int()

	if errors.Is(err, redis.Nil) {
		st.client.Set(ctx, st.limitKeyName, 1, 1*time.Second)
		return true
	} else if err != nil {
		logger.With(zap.Error(err)).Error("request rate limited")
		return false
	}

	if val >= int(st.limit.Limit) {
		return false
	}

	st.client.Incr(ctx, st.limitKeyName)

	return true
}

func WithGlobalRequestRateLimiter(store config.Store, rateLimitPerSec int32) func(http.Handler) http.Handler {
	limitStore, err := NewLimitStore(store, rateLimitPerSec)
	if err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			ctx := r.Context()
			if !limitStore.TryPassRequestLimit(ctx) {
				logger := mdlogger.FromContext(r.Context())
				logger.Warn("request rate limited")

				w.WriteHeader(http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
