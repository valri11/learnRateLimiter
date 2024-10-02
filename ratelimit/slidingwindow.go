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

type LocalSlidingWindowLimit struct {
	Limit    int32
	Requests []int64
	mx       sync.Mutex
}

func NewLocalSlidingWindowLimit(limit int) (*LocalSlidingWindowLimit, error) {
	sw := LocalSlidingWindowLimit{
		Limit: int32(limit),
	}
	return &sw, nil
}

var getTimeNowFn = time.Now

func (lws *LocalSlidingWindowLimit) TryPassRequestLimit(ctx context.Context) bool {
	logger := mdlogger.FromContext(ctx)

	lws.mx.Lock()
	defer lws.mx.Unlock()

	left := -1
	now := getTimeNowFn()
	nowUnixMicro := now.UnixMicro()
	for i := 0; i < len(lws.Requests); i++ {
		if nowUnixMicro-lws.Requests[i] >= 1000_000 {
			left++
		}
	}
	if left != -1 {
		lws.Requests = lws.Requests[left+1:]
	}

	if int(lws.Limit)-len(lws.Requests) < 1 {
		logger.
			With(zap.Int("req_count", len(lws.Requests))).
			With(zap.Int32("limit", lws.Limit)).
			Warn("breach rate limit")
		return false
	}

	lws.Requests = append(lws.Requests, nowUnixMicro)

	logger.
		With(zap.Int("req_count", len(lws.Requests))).
		With(zap.Int32("limit", lws.Limit)).
		Debug("rate limit check")

	return true
}

type RedisSlidingWindowLimit struct {
	Limit        int32
	client       redis.UniversalClient
	limitKeyName string
}

func NewRedisSlidingWindowLimit(store config.Store, rateLimitPerSec int) (*RedisSlidingWindowLimit, error) {
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

	rsw := RedisSlidingWindowLimit{
		Limit:        int32(rateLimitPerSec),
		client:       client,
		limitKeyName: "slidingWindowLimit",
	}

	return &rsw, nil
}

// redis streams implementation
func (rsw *RedisSlidingWindowLimit) TryPassRequestLimit(ctx context.Context) bool {
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

/*
// redis ordered set implementation
func (rsw *RedisSlidingWindowLimit) TryPassRequestLimit(ctx context.Context) bool {
	logger := mdlogger.FromContext(ctx)

	script := `local current_time = redis.call('TIME')
local window = ARGV[1]
local max_requests = ARGV[2]
local key = KEYS[1]
local trim_time = tonumber(current_time[1]) - window
redis.call('ZREMRANGEBYSCORE', key, 0, trim_time)
local request_count = redis.call('ZCARD',key)

if request_count < tonumber(max_requests) then
    redis.call('ZADD', key, current_time[1], current_time[1] .. current_time[2])
    redis.call('EXPIRE', key, window)
    return {0, request_count+1}
end
return {1, request_count}`

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
*/
