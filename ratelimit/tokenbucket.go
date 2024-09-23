package ratelimit

import (
	"context"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	mdlogger "github.com/valri11/go-servicepack/logger"
	"github.com/valri11/learnRateLimiter/config"
	"go.uber.org/zap"
)

type RedisTokenBucketLimit struct {
	Limit        int32
	client       redis.UniversalClient
	limitKeyName string
}

func NewRedisTokenBucketLimit(store config.Store, rateLimitPerSec int32) (*RedisTokenBucketLimit, error) {
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

	rsw := RedisTokenBucketLimit{
		Limit:        rateLimitPerSec,
		client:       client,
		limitKeyName: "tokenBucketLimit",
	}

	return &rsw, nil
}

func (rsw *RedisTokenBucketLimit) TryPassRequestLimit(ctx context.Context) bool {
	logger := mdlogger.FromContext(ctx)

	script := `local now = redis.call('TIME')
local window = tonumber(ARGV[1])
local max_requests = tonumber(ARGV[2])
local key = KEYS[1]
local now_ms = math.floor(now[1] * 1000 + now[2] / 1000);
local start_window_id = now_ms - window * 1000
local range = redis.call('XRANGE', key, start_window_id, '+')
local request_count = 0;
for _, item in ipairs(range) do
    request_count = request_count + tonumber(item[2][2])
end

if request_count >= max_requests then
    return {1,request_count} 
end

redis.call('XADD', key, 'MINID', '~', start_window_id, '*', 'count', 1)
redis.call('EXPIRE', key, window)
return {0, request_count+1}`

	ret, err := rsw.client.Eval(ctx, script,
		[]string{rsw.limitKeyName},
		1,
		rsw.Limit,
	).Slice()
	if err != nil {
		logger.With(zap.Error(err)).Error("request rate limited, check error message")
		return false
	}

	res := ret[0].(int64)
	req_count := ret[1].(int64)

	if res != 0 {
		logger.
			With(zap.Int64("req_count", req_count)).
			With(zap.Int32("limit", rsw.Limit)).
			Warn("breach rate limit")
		return false
	}

	logger.
		With(zap.Int64("req_count", req_count)).
		With(zap.Int32("limit", rsw.Limit)).
		Debug("rate limit check")

	return true
}
