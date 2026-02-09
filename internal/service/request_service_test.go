package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/robertomachorro/doormanlb/internal/cache"
	"github.com/robertomachorro/doormanlb/internal/config"
	"github.com/robertomachorro/doormanlb/internal/proxy"
	"github.com/robertomachorro/doormanlb/internal/routing"
)

func TestHandlePassthroughBypassesCache(t *testing.T) {
	cfg := config.Config{
		Services: []string{"http://svc-a"},
		Strategy: config.StrategyRoundRobin,
		Endpoints: map[string]config.EndpointConfig{
			config.DefaultEndpointKey: {CacheBehavior: config.CacheBehaviorPassthrough},
		},
	}

	router, err := routing.NewRouter(cfg.Services, cfg.Strategy)
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	store := &fakeStore{}
	fetcher := &fakeFetcher{
		response: &proxy.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-From": []string{"upstream"}},
			Body:       []byte("body"),
		},
	}

	svc := NewCachingService(cfg, router, store, fetcher)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/page?a=1", nil)
	recorder := httptest.NewRecorder()

	if err := svc.Handle(context.Background(), req, recorder); err != nil {
		t.Fatalf("handling request: %v", err)
	}

	if store.getCalled != 0 {
		t.Fatalf("expected cache get not to be called, got %d", store.getCalled)
	}
	if store.setCalled != 0 {
		t.Fatalf("expected cache set not to be called, got %d", store.setCalled)
	}
	if fetcher.called != 1 {
		t.Fatalf("expected one upstream fetch, got %d", fetcher.called)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
}

func TestHandleCacheHitSkipsUpstream(t *testing.T) {
	cfg := config.Config{
		Services: []string{"http://svc-a"},
		Strategy: config.StrategyRoundRobin,
		Endpoints: map[string]config.EndpointConfig{
			config.DefaultEndpointKey: {
				CacheBehavior: config.CacheBehaviorCache,
				ExpireTimeout: 60000,
			},
		},
	}

	router, err := routing.NewRouter(cfg.Services, cfg.Strategy)
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	store := &fakeStore{
		getResponse: &proxy.Response{
			StatusCode: http.StatusAccepted,
			Header:     http.Header{"X-Cache": []string{"hit"}},
			Body:       []byte("cached"),
		},
	}
	fetcher := &fakeFetcher{}

	svc := NewCachingService(cfg, router, store, fetcher)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/items?b=2&a=1", nil)
	recorder := httptest.NewRecorder()

	if err := svc.Handle(context.Background(), req, recorder); err != nil {
		t.Fatalf("handling request: %v", err)
	}

	if store.getCalled != 1 {
		t.Fatalf("expected one cache get call, got %d", store.getCalled)
	}
	if store.acquireCalled != 0 {
		t.Fatalf("expected no leader lock attempts on cache hit, got %d", store.acquireCalled)
	}
	if fetcher.called != 0 {
		t.Fatalf("expected no upstream calls on cache hit, got %d", fetcher.called)
	}
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d", recorder.Code)
	}
}

func TestHandleCacheMissFetchesAndStores(t *testing.T) {
	cfg := config.Config{
		Services: []string{"http://svc-a"},
		Strategy: config.StrategyRoundRobin,
		Endpoints: map[string]config.EndpointConfig{
			config.DefaultEndpointKey: {
				CacheBehavior: config.CacheBehaviorCache,
				ExpireTimeout: 1200,
			},
		},
	}

	router, err := routing.NewRouter(cfg.Services, cfg.Strategy)
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	store := &fakeStore{}
	fetcher := &fakeFetcher{
		response: &proxy.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-Upstream": []string{"true"}},
			Body:       []byte("fresh"),
		},
	}

	svc := NewCachingService(cfg, router, store, fetcher)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/articles?b=2&a=1", nil)
	recorder := httptest.NewRecorder()

	if err := svc.Handle(context.Background(), req, recorder); err != nil {
		t.Fatalf("handling request: %v", err)
	}

	if fetcher.called != 1 {
		t.Fatalf("expected one upstream call, got %d", fetcher.called)
	}
	if store.acquireCalled != 1 {
		t.Fatalf("expected one leader lock attempt, got %d", store.acquireCalled)
	}
	if store.setCalled != 1 {
		t.Fatalf("expected one cache set call, got %d", store.setCalled)
	}
	if store.publishCalled != 1 {
		t.Fatalf("expected one done publish, got %d", store.publishCalled)
	}
	if store.releaseCalled != 1 {
		t.Fatalf("expected one lock release, got %d", store.releaseCalled)
	}
	if store.lastTTL != 1200*time.Millisecond {
		t.Fatalf("expected ttl 1200ms, got %s", store.lastTTL)
	}
	if recorder.Body.String() != "fresh" {
		t.Fatalf("unexpected response body %q", recorder.Body.String())
	}
}

func TestHandleCacheMissDoesNotStore5xx(t *testing.T) {
	cfg := config.Config{
		Services: []string{"http://svc-a"},
		Strategy: config.StrategyRoundRobin,
		Endpoints: map[string]config.EndpointConfig{
			config.DefaultEndpointKey: {
				CacheBehavior: config.CacheBehaviorCache,
				ExpireTimeout: 5000,
			},
		},
	}

	router, err := routing.NewRouter(cfg.Services, cfg.Strategy)
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	store := &fakeStore{}
	fetcher := &fakeFetcher{
		response: &proxy.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       []byte("error"),
		},
	}

	svc := NewCachingService(cfg, router, store, fetcher)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/articles?a=1", nil)
	recorder := httptest.NewRecorder()

	if err := svc.Handle(context.Background(), req, recorder); err != nil {
		t.Fatalf("handling request: %v", err)
	}

	if store.setCalled != 0 {
		t.Fatalf("expected no cache set for 5xx, got %d", store.setCalled)
	}
	metrics := svc.Metrics()
	if metrics["cache_skips_5xx_total"] != 1 {
		t.Fatalf("expected cache_skips_5xx_total=1, got %d", metrics["cache_skips_5xx_total"])
	}
}

func TestHandleCacheMissFollowerWaitsAndUsesCache(t *testing.T) {
	cfg := config.Config{
		Services: []string{"http://svc-a"},
		Strategy: config.StrategyRoundRobin,
		Endpoints: map[string]config.EndpointConfig{
			config.DefaultEndpointKey: {
				CacheBehavior: config.CacheBehaviorCache,
				ExpireTimeout: 5000,
			},
		},
	}

	router, err := routing.NewRouter(cfg.Services, cfg.Strategy)
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	store := &fakeStore{
		getResponses: []*proxy.Response{
			nil,
			{StatusCode: http.StatusCreated, Body: []byte("after-wait")},
		},
		forceFollower: true,
	}
	fetcher := &fakeFetcher{}

	svc := NewCachingService(cfg, router, store, fetcher)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/articles?a=1", nil)
	recorder := httptest.NewRecorder()

	if err := svc.Handle(context.Background(), req, recorder); err != nil {
		t.Fatalf("handling request: %v", err)
	}

	if store.waitCalled != 1 {
		t.Fatalf("expected one wait call, got %d", store.waitCalled)
	}
	if fetcher.called != 0 {
		t.Fatalf("expected no upstream call for follower cache hit, got %d", fetcher.called)
	}
	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d", recorder.Code)
	}
}

func TestHandleCacheMissFollowerTimeoutFallsBackToFetch(t *testing.T) {
	cfg := config.Config{
		Services: []string{"http://svc-a"},
		Strategy: config.StrategyRoundRobin,
		Endpoints: map[string]config.EndpointConfig{
			config.DefaultEndpointKey: {
				CacheBehavior: config.CacheBehaviorCache,
				ExpireTimeout: 5000,
			},
		},
	}

	router, err := routing.NewRouter(cfg.Services, cfg.Strategy)
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	store := &fakeStore{
		forceFollower: true,
		waitErr:       cache.ErrWaitTimeout,
	}
	fetcher := &fakeFetcher{response: &proxy.Response{StatusCode: http.StatusOK, Body: []byte("fallback")}}

	svc := NewCachingService(cfg, router, store, fetcher)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/articles?a=1", nil)
	recorder := httptest.NewRecorder()

	if err := svc.Handle(context.Background(), req, recorder); err != nil {
		t.Fatalf("handling request: %v", err)
	}

	if fetcher.called != 1 {
		t.Fatalf("expected fallback upstream call, got %d", fetcher.called)
	}
	if recorder.Body.String() != "fallback" {
		t.Fatalf("expected fallback body, got %q", recorder.Body.String())
	}
}

func TestReadyFailsWhenCacheConfiguredButMissingStore(t *testing.T) {
	cfg := config.Config{
		Services: []string{"http://svc-a"},
		Strategy: config.StrategyRoundRobin,
		Endpoints: map[string]config.EndpointConfig{
			config.DefaultEndpointKey: {
				CacheBehavior: config.CacheBehaviorCache,
				ExpireTimeout: 5000,
			},
		},
	}

	router, err := routing.NewRouter(cfg.Services, cfg.Strategy)
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	svc := NewCachingService(cfg, router, nil, &fakeFetcher{})
	if err := svc.Ready(context.Background()); err == nil {
		t.Fatal("expected readiness error when cache is configured without store")
	}
}

type fakeStore struct {
	getCalled     int
	acquireCalled int
	releaseCalled int
	publishCalled int
	waitCalled    int
	setCalled     int
	getResponse   *proxy.Response
	getResponses  []*proxy.Response
	getErr        error
	setErr        error
	acquireErr    error
	forceFollower bool
	waitErr       error
	lastKey       string
	lastResponse  *proxy.Response
	lastTTL       time.Duration
	lastLockTTL   time.Duration
	lastLock      *cache.Lock
}

func (f *fakeStore) Get(_ context.Context, key string) (*proxy.Response, error) {
	f.getCalled++
	f.lastKey = key
	if len(f.getResponses) > 0 {
		response := f.getResponses[0]
		f.getResponses = f.getResponses[1:]
		return response, f.getErr
	}
	return f.getResponse, f.getErr
}

func (f *fakeStore) Set(_ context.Context, key string, response *proxy.Response, ttl time.Duration) error {
	f.setCalled++
	f.lastKey = key
	f.lastResponse = response
	f.lastTTL = ttl
	return f.setErr
}

func (f *fakeStore) TryAcquireLeader(_ context.Context, key string, ttl time.Duration) (*cache.Lock, bool, error) {
	f.acquireCalled++
	f.lastKey = key
	f.lastLockTTL = ttl
	if f.acquireErr != nil {
		return nil, false, f.acquireErr
	}

	if f.forceFollower {
		return nil, false, nil
	}

	lock := &cache.Lock{Key: key, Token: "token"}
	f.lastLock = lock
	return lock, true, nil
}

func (f *fakeStore) ReleaseLeader(_ context.Context, lock *cache.Lock) error {
	f.releaseCalled++
	f.lastLock = lock
	return nil
}

func (f *fakeStore) PublishDone(_ context.Context, _ string) error {
	f.publishCalled++
	return nil
}

func (f *fakeStore) WaitForDone(_ context.Context, _ string, _ time.Duration) error {
	f.waitCalled++
	if f.waitErr != nil {
		return f.waitErr
	}
	return nil
}

func (f *fakeStore) Ping(_ context.Context) error {
	return nil
}

type fakeFetcher struct {
	called   int
	response *proxy.Response
	err      error
}

func (f *fakeFetcher) Fetch(_ context.Context, _ string, _ *http.Request) (*proxy.Response, error) {
	f.called++
	if f.response == nil {
		f.response = &proxy.Response{StatusCode: http.StatusOK}
	}
	return f.response, f.err
}
