package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/robertomachorro/doormanlb/internal/cache"
	"github.com/robertomachorro/doormanlb/internal/config"
	"github.com/robertomachorro/doormanlb/internal/keybuilder"
	"github.com/robertomachorro/doormanlb/internal/proxy"
	"github.com/robertomachorro/doormanlb/internal/routing"
)

type RequestService interface {
	Handle(ctx context.Context, request *http.Request, writer http.ResponseWriter) error
}

type responseFetcher interface {
	Fetch(ctx context.Context, upstreamBaseURL string, request *http.Request) (*proxy.Response, error)
}

type CachingService struct {
	config config.Config
	router *routing.Router
	cache  cache.Store
	proxy  responseFetcher
}

const (
	defaultLeaderLockTTL = 15 * time.Second
)

func NewCachingService(config config.Config, router *routing.Router, cacheStore cache.Store, proxyClient responseFetcher) *CachingService {
	return &CachingService{
		config: config,
		router: router,
		cache:  cacheStore,
		proxy:  proxyClient,
	}
}

func (s *CachingService) Handle(ctx context.Context, request *http.Request, writer http.ResponseWriter) error {
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

	for attempts := 0; attempts < 2; attempts++ {
		cachedResponse, err := s.cache.Get(ctx, cacheKey)
		if err != nil {
			return err
		}
		if cachedResponse != nil {
			cachedResponse.WriteTo(writer)
			return nil
		}

		lock, acquired, err := s.cache.TryAcquireLeader(ctx, cacheKey, lockTTL)
		if err != nil {
			return err
		}
		if acquired {
			return s.handleAsLeader(ctx, request, writer, cacheKey, ttl, lock)
		}

		// A winner already exists. Wait for completion, then retry cache read.
		err = s.cache.WaitForDone(ctx, cacheKey, lockTTL)
		if err != nil && !errors.Is(err, cache.ErrWaitTimeout) {
			return err
		}
	}

	// Fallback to direct upstream response if lock/wait retries were inconclusive.
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

	if err := s.cache.Set(ctx, cacheKey, upstreamResponse, ttl); err != nil {
		// Best effort in phase 3: serve the response even if cache storage fails.
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
	lease := s.router.Acquire()
	defer lease.Release()

	return s.proxy.Fetch(ctx, lease.URL, request)
}

func leaderLockTTL(cacheTTL time.Duration) time.Duration {
	if cacheTTL > defaultLeaderLockTTL {
		return cacheTTL
	}
	return defaultLeaderLockTTL
}
