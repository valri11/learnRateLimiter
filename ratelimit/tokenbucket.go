package ratelimit

import (
	"context"
	"strconv"
	"sync"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	mdlogger "github.com/valri11/go-servicepack/logger"
	"github.com/valri11/learnRateLimiter/config"
	"go.uber.org/zap"
)

var _ LimitStore = (*LocalTokenBucketLimit)(nil)

type LocalTokenBucketLimit struct {
	Capacity       int64
	RefillRateMs   int64
	Tokens         int64
	LastRefillTime int64
	mx             sync.Mutex
}

func NewLocalTokenBucketLimitFromStore(store config.Store) (*LocalTokenBucketLimit, error) {
	rateLimitPerSec, err := strconv.Atoi(store.Parameters["ratelimitpersec"])
	if err != nil {
		return nil, err
	}
	return NewLocalTokenBucketLimit(int32(rateLimitPerSec))
}

func NewLocalTokenBucketLimit(rateLimitPerSec int32) (*LocalTokenBucketLimit, error) {
	tb := LocalTokenBucketLimit{
		Capacity:     int64(rateLimitPerSec),
		RefillRateMs: int64(1000 / rateLimitPerSec),
		Tokens:       int64(rateLimitPerSec),
	}
	return &tb, nil
}

func (tb *LocalTokenBucketLimit) TryPassRequestLimit(ctx context.Context) RequestLimitAllowance {
	tb.mx.Lock()
	defer tb.mx.Unlock()

	res := RequestLimitAllowance{
		Allowed:        false,
		Limit:          tb.Capacity,
		LimitWindowSec: 1,
		Remaining:      0,
	}

	nowMs := getTimeNowFn().UnixMilli()

	// refill tokens based on elapsed time since last refill
	elapsedMs := nowMs - tb.LastRefillTime
	addTokens := elapsedMs / tb.RefillRateMs

	if addTokens > 0 {
		tb.Tokens = min(tb.Capacity, tb.Tokens+addTokens)
		if addTokens < tb.Capacity {
			tb.LastRefillTime += addTokens * tb.RefillRateMs
		} else {
			tb.LastRefillTime = nowMs
		}
	}

	if tb.Tokens < 1 {
		return res
	}

	tb.Tokens--

	res.Allowed = true
	res.Remaining = tb.Tokens

	return res
}

type RedisTokenBucketLimit struct {
	Capacity     int64
	RefillRateMs int32
	client       redis.UniversalClient
	limitKeyName string
}

func NewRedisTokenBucketLimit(store config.Store) (*RedisTokenBucketLimit, error) {
	rateLimitPerSec, err := strconv.Atoi(store.Parameters["ratelimitpersec"])
	if err != nil {
		return nil, err
	}

	// from rateLimitPerSec
	// capacity = rateLimitPerSec
	// refill_rate = 1000 / rateLimitPerSec
	// example:
	// rateLimitPerSec = 10
	// 10 requests per second
	// capacity = 10
	// refill_rate = 100ms

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

	rsw := RedisTokenBucketLimit{
		Capacity:     int64(rateLimitPerSec),
		RefillRateMs: int32(1000 / rateLimitPerSec),
		client:       client,
		limitKeyName: "tokenBucketLimit",
	}

	return &rsw, nil
}

func (rsw *RedisTokenBucketLimit) TryPassRequestLimit(ctx context.Context) RequestLimitAllowance {
	logger := mdlogger.FromContext(ctx)

	res := RequestLimitAllowance{
		Allowed:        false,
		Limit:          rsw.Capacity,
		LimitWindowSec: 1,
		Remaining:      0,
	}

	script := `local now = redis.call('TIME')
local capacity = tonumber(ARGV[1])
local refill_rate_ms = tonumber(ARGV[2])
local key_tokens = KEYS[1] .. "tokens"
local key_last_access = KEYS[1] .. "last_access"

local requested_tokens = 1

local now_ms = math.floor(now[1] * 1000 + now[2] / 1000);

-- fetch tokens in the bucket
local last_tokens = tonumber(redis.call("GET", key_tokens))
if last_tokens == nil then
    last_tokens = capacity
end

-- fetch the last access time
local last_access_ms = tonumber(redis.call("GET", key_last_access))
if last_access_ms == nil then
    last_access_ms = 0
end

-- Calculate the number of tokens to be added due to the elapsed time since the
-- last access. We cap the number at the capacity of the bucket.
local elapsed_ms = math.max(0, now_ms - last_access_ms)
local add_tokens = math.floor(elapsed_ms / refill_rate_ms)
local new_tokens = math.min(capacity, last_tokens + add_tokens)

-- Calculate the new last access time. We don't want to use the current time as
-- the new last access time, because that would result in a rounding error.
local new_access_time_ms = last_access_ms + math.ceil(add_tokens * refill_rate_ms)
--local new_access_time_ms = now_ms

-- Check if enough tokens have been accumulated
local allowed = new_tokens >= requested_tokens
if allowed then
    new_tokens = new_tokens - requested_tokens
end

-- Update state
redis.call("SET", key_tokens, new_tokens, "EX", 2)
redis.call("SET", key_last_access, new_access_time_ms, "EX", 2)

local request_count = 0;

return {allowed and 1 or 0, capacity - new_tokens}`

	ret, err := rsw.client.Eval(ctx, script,
		[]string{rsw.limitKeyName},
		rsw.Capacity,
		rsw.RefillRateMs,
	).Slice()
	if err != nil {
		logger.With(zap.Error(err)).Error("request rate limited, check error message")
		return res
	}

	allowed := ret[0].(int64)
	req_count := ret[1].(int64)

	if allowed == 0 {
		logger.
			With(zap.Int64("req_count", req_count)).
			With(zap.Int64("limit", rsw.Capacity)).
			Warn("breach rate limit")
		return res
	}

	res.Allowed = true
	res.Remaining = res.Limit - req_count

	logger.
		With(zap.Int64("req_count", req_count)).
		With(zap.Int64("limit", rsw.Capacity)).
		Debug("rate limit check")

	return res
}
