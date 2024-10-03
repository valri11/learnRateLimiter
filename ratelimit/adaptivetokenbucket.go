package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	mdlogger "github.com/valri11/go-servicepack/logger"
	"github.com/valri11/learnRateLimiter/config"
	"go.uber.org/zap"
)

type RateLimitTier struct {
	Capacity           int64
	RefillInterval     int64
	WindowDuration     int64
	NextTierRejectRate int
}

type LocalAdaptiveTokenBucketLimit struct {
	mx             sync.Mutex
	Tiers          []RateLimitTier
	CurrentTierIdx int
	WindowStartTs  int64
	RejectCounter  int64
	AllowCounter   int64
	LastRefillTs   int64
	UsedTokens     int64
}

func NewLocalAdaptiveTokenBucketLimit(store config.Store, rateLimitPerSec int) (*LocalAdaptiveTokenBucketLimit, error) {

	// parse tiers
	tiers := make([]RateLimitTier, 0)

	tiersCfg := store.Parameters["tiers"]
	tiersCfgList := strings.Split(tiersCfg, ";")
	for idx, tc := range tiersCfgList {
		if idx+1 == len(tiersCfgList) && tc == "" {
			// allow closing ";"
			break
		}
		tcp := strings.Split(tc, ",")
		if len(tcp) != 4 {
			return nil, fmt.Errorf("invalid tier config: %s", tc)
		}
		capacity, err := strconv.Atoi(tcp[0])
		if err != nil {
			return nil, fmt.Errorf("invalid tier config (capacity): %s, err: %v", tc, err)
		}

		if idx > 0 {
			// sanity check increasing capacity
			if int64(capacity) < tiers[idx-1].Capacity {
				return nil, fmt.Errorf("tiers should have increasing capacity")
			}
		}

		refillInterval, err := time.ParseDuration(tcp[1])
		if err != nil {
			return nil, fmt.Errorf("invalid tier config (refillInterval): %s, err: %v", tc, err)
		}

		window, err := time.ParseDuration(tcp[2])
		if err != nil {
			return nil, fmt.Errorf("invalid tier config (window): %s, err: %v", tc, err)
		}

		nextTierRejectRate, err := strconv.Atoi(tcp[3])
		if err != nil {
			return nil, fmt.Errorf("invalid tier config (nextTierRejectRate): %s, err: %v", tc, err)
		}

		tiers = append(tiers, RateLimitTier{
			Capacity:           int64(capacity),
			RefillInterval:     refillInterval.Milliseconds(),
			WindowDuration:     window.Milliseconds(),
			NextTierRejectRate: nextTierRejectRate,
		})
	}

	tbl := LocalAdaptiveTokenBucketLimit{
		Tiers: tiers,
	}

	return &tbl, nil
}

func (st *LocalAdaptiveTokenBucketLimit) TryPassRequestLimit(ctx context.Context) bool {
	st.mx.Lock()
	defer st.mx.Unlock()

	logger := mdlogger.FromContext(ctx)

	tsNow := getTimeNowFn().UnixMilli()

	// check if window completed and needs to advance to the next tier
	//windowTokens := st.Tiers[st.CurrentTierIdx].WindowDuration / st.Tiers[st.CurrentTierIdx].RefillInterval
	windowTokens := (tsNow - st.WindowStartTs) / st.Tiers[st.CurrentTierIdx].RefillInterval
	if tsNow-st.WindowStartTs >= st.Tiers[st.CurrentTierIdx].WindowDuration {
		errorRate := 0
		if windowTokens > 0 {
			errorRate = int(100 * st.RejectCounter / windowTokens)
		}
		if errorRate >= st.Tiers[st.CurrentTierIdx].NextTierRejectRate {
			// client request rate is above error threshold
			// we advance to a next tier with a higher rate limit
			if st.CurrentTierIdx+1 < len(st.Tiers) {
				st.CurrentTierIdx++
				// start new tier
				st.WindowStartTs = tsNow
				st.LastRefillTs = 0
				st.RejectCounter = 0
				st.AllowCounter = 0
				st.UsedTokens = 0

				logger.
					With(zap.Int64("capacity", st.Tiers[st.CurrentTierIdx].Capacity)).
					Debug("tier up")
			}
		} else {
			// try to downgrade tier to lesser rate
			if st.CurrentTierIdx != 0 {
				windowUtilizationRate := 0.0
				if windowTokens > 0 {
					windowUtilizationRate = float64(st.AllowCounter) / float64(windowTokens)
				}
				if windowUtilizationRate < 0.1 {
					st.CurrentTierIdx = 0
					logger.
						With(zap.Int64("capacity", st.Tiers[st.CurrentTierIdx].Capacity)).
						Debug("tier reset")
				} else if windowUtilizationRate < 0.8 {
					st.CurrentTierIdx--
					logger.
						With(zap.Int64("capacity", st.Tiers[st.CurrentTierIdx].Capacity)).
						Debug("tier down")
				}
			}
			st.WindowStartTs = tsNow
			st.LastRefillTs = 0
			st.RejectCounter = 0
			st.AllowCounter = 0
			st.UsedTokens = 0
		}
	}

	currentTier := &st.Tiers[st.CurrentTierIdx]

	// how many tokens needs to be added since last refill
	elapsed := tsNow - st.LastRefillTs
	addTokens := elapsed / currentTier.RefillInterval

	if addTokens > 0 {
		st.UsedTokens = max(0, st.UsedTokens-addTokens)
		if addTokens < int64(st.Tiers[st.CurrentTierIdx].Capacity) {
			st.LastRefillTs += addTokens * currentTier.RefillInterval
		} else {
			st.LastRefillTs = tsNow
		}
	}

	// check if request allowed
	allowed := st.UsedTokens < int64(currentTier.Capacity)

	st.UsedTokens = min(int64(currentTier.Capacity), st.UsedTokens+1)

	if !allowed {
		st.RejectCounter++
		/*
			logger.
				With(zap.Int64("req_count", st.UsedTokens)).
				With(zap.Int64("limit", currentTier.Capacity)).
				With(zap.Int64("window_reject_count", st.RejectCounter)).
				Warn("breach rate limit")
		*/
		return false
	}
	st.AllowCounter++

	/*
		logger.
			With(zap.Int64("req_count", st.UsedTokens)).
			With(zap.Int64("limit", currentTier.Capacity)).
			With(zap.Int64("window_reject_count", st.RejectCounter)).
			Debug("rate limit check")
	*/

	return true
}

type RedisAdaptiveTokenBucketLimit struct {
	Tiers        []RateLimitTier
	client       redis.UniversalClient
	limitKeyName string
}

func NewRedisAdaptiveTokenBucketLimit(store config.Store, rateLimitPerSec int) (*RedisAdaptiveTokenBucketLimit, error) {
	// parse tiers
	tiers := make([]RateLimitTier, 0)

	tiersCfg := store.Parameters["tiers"]
	tiersCfgList := strings.Split(tiersCfg, ";")
	for idx, tc := range tiersCfgList {
		if idx+1 == len(tiersCfgList) && tc == "" {
			// allow closing ";"
			break
		}
		tcp := strings.Split(tc, ",")
		if len(tcp) != 4 {
			return nil, fmt.Errorf("invalid tier config: %s", tc)
		}
		capacity, err := strconv.Atoi(tcp[0])
		if err != nil {
			return nil, fmt.Errorf("invalid tier config (capacity): %s, err: %v", tc, err)
		}

		if idx > 0 {
			// sanity check increasing capacity
			if int64(capacity) < tiers[idx-1].Capacity {
				return nil, fmt.Errorf("tiers should have increasing capacity")
			}
		}

		refillInterval, err := time.ParseDuration(tcp[1])
		if err != nil {
			return nil, fmt.Errorf("invalid tier config (refillInterval): %s, err: %v", tc, err)
		}

		window, err := time.ParseDuration(tcp[2])
		if err != nil {
			return nil, fmt.Errorf("invalid tier config (window): %s, err: %v", tc, err)
		}

		nextTierRejectRate, err := strconv.Atoi(tcp[3])
		if err != nil {
			return nil, fmt.Errorf("invalid tier config (nextTierRejectRate): %s, err: %v", tc, err)
		}

		tiers = append(tiers, RateLimitTier{
			Capacity:           int64(capacity),
			RefillInterval:     refillInterval.Milliseconds(),
			WindowDuration:     window.Milliseconds(),
			NextTierRejectRate: nextTierRejectRate,
		})
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

	err := client.Ping(ctx).Err()
	if err != nil {
		return nil, err
	}

	tbl := RedisAdaptiveTokenBucketLimit{
		Tiers:        tiers,
		client:       client,
		limitKeyName: "adaptiveTokenBucketLimit",
	}

	return &tbl, nil
}

func (st *RedisAdaptiveTokenBucketLimit) TryPassRequestLimit(ctx context.Context) bool {

	logger := mdlogger.FromContext(ctx)

	script := `local now = redis.call('TIME')
local key_bucket = KEYS[1] .. "_bucket"
local szTiers = tonumber(ARGV[1])

local now_ms = math.floor(now[1] * 1000 + now[2] / 1000);

local tiers = {}

for i=1,szTiers do
	local tier = {}
	tier['capacity'] = tonumber(ARGV[1 + (i-1)*4 + 1])
	tier['refillInterval'] = tonumber(ARGV[1 + (i-1)*4 + 2])
	tier['windowDuration'] = tonumber(ARGV[1 + (i-1)*4 + 3])
	tier['nextTierRejectRate'] = tonumber(ARGV[1 + (i-1)*4 + 4])

	tiers[i] = tier
end

local bucketList = redis.call('HMGET', key_bucket, 
	'currentTierIdx', 'windowStartTs', 'rejectCounter',
	'allowCounter', 'lastRefillTs', 'usedTokens')

local currentTierIdx = tonumber(bucketList[1]) or 1
local windowStartTs = tonumber(bucketList[2]) or 0
local rejectCounter = tonumber(bucketList[3]) or 0
local allowCounter = tonumber(bucketList[4]) or 0
local lastRefillTs = tonumber(bucketList[5]) or 0
local usedTokens = tonumber(bucketList[6]) or 0

local prevTierIdx = currentTierIdx

local windowTokens = math.floor((now_ms - windowStartTs) / tiers[currentTierIdx]['refillInterval'])
local errorRate = 0
if windowTokens > 0 then
	errorRate = 100 * rejectCounter / windowTokens
end
if now_ms-windowStartTs >= tiers[currentTierIdx]['windowDuration'] then
	if errorRate >= tiers[currentTierIdx]['nextTierRejectRate'] then
		if currentTierIdx+1 <= szTiers then
            currentTierIdx = currentTierIdx + 1
            -- start new tier
            windowStartTs = now_ms
            lastRefillTs = 0
            rejectCounter = 0
            allowCounter = 0
            usedTokens = 0
        end
	else
		-- try to downgrade tier to lesser rate
		if currentTierIdx ~= 1 then
            local windowUtilizationRate = 0
			if windowTokens > 0 then
				windowUtilizationRate = allowCounter / windowTokens
			end
            if windowUtilizationRate < 0.1 then
                currentTierIdx = 1
            elseif windowUtilizationRate < 0.8 then
                currentTierIdx = currentTierIdx - 1
            end
        end

        windowStartTs = now_ms
        lastRefillTs = 0
        rejectCounter = 0
    	allowCounter = 0
        usedTokens = 0
	end
end

local currentTier = tiers[currentTierIdx]

-- how many tokens needs to be added since last refill
local elapsed = now_ms - lastRefillTs
local addTokens = math.floor(elapsed / currentTier['refillInterval'])

if addTokens > 0 then
	usedTokens = math.max(0, usedTokens - addTokens)
    if addTokens < currentTier['capacity'] then
        lastRefillTs = lastRefillTs + addTokens * currentTier['refillInterval']
    else
        lastRefillTs = now_ms
    end
end

-- check if request allowed
local allowed = usedTokens < currentTier['capacity']

usedTokens = math.min(currentTier['capacity'], usedTokens + 1)

if allowed then
	allowCounter = allowCounter + 1
else
	rejectCounter = rejectCounter + 1
end

local bucket = {}
bucket['currentTierIdx'] = currentTierIdx
bucket['windowStartTs'] = windowStartTs
bucket['now_ms'] = now_ms
bucket['rejectCounter'] = rejectCounter
bucket['allowCounter'] = allowCounter
bucket['lastRefillTs'] = lastRefillTs
bucket['usedTokens'] = usedTokens
bucket['windowTokens'] = windowTokens
bucket['errorRate'] = errorRate

redis.call('HMSET', key_bucket, 
	'currentTierIdx', currentTierIdx,
	'windowStartTs', windowStartTs,
	'rejectCounter', rejectCounter,
	'allowCounter', allowCounter,
	'lastRefillTs', lastRefillTs,
	'usedTokens', usedTokens)

redis.call('EXPIRE', key_bucket, 2 * currentTier['windowDuration'] / 1000)

return {allowed and 0 or 1, usedTokens, currentTierIdx - 1, prevTierIdx - 1, szTiers, cjson.encode(bucket)}
`

	tiersArgs := make([]any, 0)
	tiersArgs = append(tiersArgs, len(st.Tiers))
	for _, t := range st.Tiers {
		tiersArgs = append(tiersArgs, t.Capacity)
		tiersArgs = append(tiersArgs, t.RefillInterval)
		tiersArgs = append(tiersArgs, t.WindowDuration)
		tiersArgs = append(tiersArgs, int64(t.NextTierRejectRate))
	}

	ret, err := st.client.Eval(ctx, script,
		[]string{st.limitKeyName},
		//len(st.Tiers),
		tiersArgs...,
	).Slice()
	if err != nil {
		logger.With(zap.Error(err)).Error("request rate limited, check error message")
		return false
	}

	res := ret[0].(int64)
	req_count := ret[1].(int64)
	currentTierIdx := ret[2].(int64)
	prevTierIdx := ret[3].(int64)

	if currentTierIdx != prevTierIdx {
		logger.
			With(zap.Int64("req_count", req_count)).
			With(zap.Int64("prevTierIdx", prevTierIdx)).
			With(zap.Int64("currentTierIdx", currentTierIdx)).
			With(zap.Int64("limit", st.Tiers[currentTierIdx].Capacity)).
			Warn("change limit tier")
	}

	if res != 0 {
		/*
			logger.
				With(zap.Int64("req_count", req_count)).
				With(zap.Int64("limit", st.Tiers[currentTierIdx].Capacity)).
				Warn("breach rate limit")
		*/
		return false
	}

	/*
		logger.
			With(zap.Int64("req_count", req_count)).
			With(zap.Int64("limit", st.Tiers[currentTierIdx].Capacity)).
			Debug("rate limit check")
	*/

	return true
}
