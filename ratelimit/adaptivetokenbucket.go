package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

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
		errorRate := int(100 * st.RejectCounter / windowTokens)
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
				windowUtilizationRate := float32(st.AllowCounter) / float32(windowTokens)
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

	tbl := RedisAdaptiveTokenBucketLimit{
		//Tiers: tiers,
	}

	return &tbl, nil
}

func (st *RedisAdaptiveTokenBucketLimit) TryPassRequestLimit(ctx context.Context) bool {

	return false
}
