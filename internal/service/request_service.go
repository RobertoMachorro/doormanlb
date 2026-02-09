package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/robertomachorro/doormanlb/internal/cache"
	"github.com/robertomachorro/doormanlb/internal/config"
	"github.com/robertomachorro/doormanlb/internal/keybuilder"
	"github.com/robertomachorro/doormanlb/internal/proxy"
	"github.com/robertomachorro/doormanlb/internal/routing"
)

type RequestService interface {
	Handle(ctx context.Context, request *http.Request, writer http.ResponseWriter) error
	Ready(ctx context.Context) error
	Metrics() map[string]uint64
}

type responseFetcher interface {
	Fetch(ctx context.Context, upstreamBaseURL string, request *http.Request) (*proxy.Response, error)
}

type CachingService struct {
	config config.Config
	router *routing.Router
	cache  cache.Store
	proxy  responseFetcher
	stats  serviceMetrics
}

const (
	defaultLeaderLockTTL = 15 * time.Second
	maxLeaderLockTTL     = 30 * time.Second
	maxCacheAttempts     = 3
)

type serviceMetrics struct {
	requestsTotal       atomic.Uint64
	cacheHits           atomic.Uint64
	cacheMisses         atomic.Uint64
	leaderAcquired      atomic.Uint64
	followerWaits       atomic.Uint64
	upstreamFetches     atomic.Uint64
	cacheSets           atomic.Uint64
	cacheSkips5xx       atomic.Uint64
	cacheOperationError atomic.Uint64
	followerTimeouts    atomic.Uint64
	fallbackFetches     atomic.Uint64
}

func NewCachingService(config config.Config, router *routing.Router, cacheStore cache.Store, proxyClient responseFetcher) *CachingService {
	return &CachingService{
		config: config,
		router: router,
		cache:  cacheStore,
		proxy:  proxyClient,
	}
}

func (s *CachingService) Handle(ctx context.Context, request *http.Request, writer http.ResponseWriter) error {
	s.stats.requestsTotal.Add(1)
	endpoint := s.config.Endpoint(request.URL.Path)

	switch endpoint.CacheBehavior {
	case config.CacheBehaviorPassthrough:
		return s.fetchAndWrite(ctx, request, writer)
	case config.CacheBehaviorCache:
		return s.handleCache(ctx, request, writer, endpoint)
	default:
		return fmt.Errorf("unsupported cache behavior %q", endpoint.CacheBehavior)
	}
}

func (s *CachingService) handleCache(ctx context.Context, request *http.Request, writer http.ResponseWriter, endpoint config.EndpointConfig) error {
	if s.cache == nil {
		return errors.New("cache behavior requires redis store")
	}

	cacheKey := keybuilder.Build(request, keybuilder.Options{IgnoreParameters: endpoint.ShouldIgnoreParameters()})
	ttl := endpoint.CacheTTL()
	lockTTL := leaderLockTTL(ttl)

	for attempts := 0; attempts < maxCacheAttempts; attempts++ {
		cachedResponse, err := s.cache.Get(ctx, cacheKey)
		if err != nil {
			s.stats.cacheOperationError.Add(1)
			return err
		}
		if cachedResponse != nil {
			s.stats.cacheHits.Add(1)
			cachedResponse.WriteTo(writer)
			return nil
		}
		s.stats.cacheMisses.Add(1)

		lock, acquired, err := s.cache.TryAcquireLeader(ctx, cacheKey, lockTTL)
		if err != nil {
			s.stats.cacheOperationError.Add(1)
			return err
		}
		if acquired {
			s.stats.leaderAcquired.Add(1)
			return s.handleAsLeader(ctx, request, writer, cacheKey, ttl, lock)
		}

		// A winner already exists. Wait for completion, then retry cache read.
		s.stats.followerWaits.Add(1)
		err = s.cache.WaitForDone(ctx, cacheKey, lockTTL)
		if err != nil && !errors.Is(err, cache.ErrWaitTimeout) {
			s.stats.cacheOperationError.Add(1)
			return err
		}
		if errors.Is(err, cache.ErrWaitTimeout) {
			s.stats.followerTimeouts.Add(1)
			if sleepErr := sleepBackoff(ctx, attempts); sleepErr != nil {
				return sleepErr
			}
		}
	}

	// Fallback to direct upstream response if lock/wait retries were inconclusive.
	s.stats.fallbackFetches.Add(1)
	return s.fetchAndWrite(ctx, request, writer)
}

func (s *CachingService) handleAsLeader(ctx context.Context, request *http.Request, writer http.ResponseWriter, cacheKey string, ttl time.Duration, lock *cache.Lock) error {
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.cache.PublishDone(cleanupCtx, cacheKey)
		_ = s.cache.ReleaseLeader(cleanupCtx, lock)
	}()

	upstreamResponse, err := s.fetchFromUpstream(ctx, request)
	if err != nil {
		return err
	}

	if shouldCache(upstreamResponse.StatusCode) {
		if err := s.cache.Set(ctx, cacheKey, upstreamResponse, ttl); err != nil {
			s.stats.cacheOperationError.Add(1)
			// Best effort: serve the response even if cache storage fails.
		} else {
			s.stats.cacheSets.Add(1)
		}
	} else {
		s.stats.cacheSkips5xx.Add(1)
	}

	upstreamResponse.WriteTo(writer)
	return nil
}

func (s *CachingService) fetchAndWrite(ctx context.Context, request *http.Request, writer http.ResponseWriter) error {
	upstreamResponse, err := s.fetchFromUpstream(ctx, request)
	if err != nil {
		return err
	}
	upstreamResponse.WriteTo(writer)
	return nil
}

func (s *CachingService) fetchFromUpstream(ctx context.Context, request *http.Request) (*proxy.Response, error) {
	s.stats.upstreamFetches.Add(1)
	lease := s.router.Acquire()
	defer lease.Release()

	return s.proxy.Fetch(ctx, lease.URL, request)
}

func (s *CachingService) Ready(ctx context.Context) error {
	if s.config.UsesCache() && s.cache == nil {
		return errors.New("cache configured but redis store is not initialized")
	}
	if checker, ok := s.cache.(interface{ Ping(context.Context) error }); ok {
		return checker.Ping(ctx)
	}
	return nil
}

func (s *CachingService) Metrics() map[string]uint64 {
	return map[string]uint64{
		"requests_total":          s.stats.requestsTotal.Load(),
		"cache_hits_total":        s.stats.cacheHits.Load(),
		"cache_misses_total":      s.stats.cacheMisses.Load(),
		"leader_acquired_total":   s.stats.leaderAcquired.Load(),
		"follower_waits_total":    s.stats.followerWaits.Load(),
		"upstream_fetches_total":  s.stats.upstreamFetches.Load(),
		"cache_sets_total":        s.stats.cacheSets.Load(),
		"cache_skips_5xx_total":   s.stats.cacheSkips5xx.Load(),
		"cache_errors_total":      s.stats.cacheOperationError.Load(),
		"follower_timeouts_total": s.stats.followerTimeouts.Load(),
		"fallback_fetches_total":  s.stats.fallbackFetches.Load(),
	}
}

func leaderLockTTL(cacheTTL time.Duration) time.Duration {
	if cacheTTL <= 0 {
		return defaultLeaderLockTTL
	}
	if cacheTTL < defaultLeaderLockTTL {
		return defaultLeaderLockTTL
	}
	if cacheTTL > maxLeaderLockTTL {
		return maxLeaderLockTTL
	}
	return cacheTTL
}

func shouldCache(statusCode int) bool {
	return statusCode < http.StatusInternalServerError
}

func sleepBackoff(ctx context.Context, attempt int) error {
	backoff := time.Duration(attempt+1) * 10 * time.Millisecond
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
