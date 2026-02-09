package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	if store.setCalled != 1 {
		t.Fatalf("expected one cache set call, got %d", store.setCalled)
	}
	if store.lastTTL != 1200*time.Millisecond {
		t.Fatalf("expected ttl 1200ms, got %s", store.lastTTL)
	}
	if recorder.Body.String() != "fresh" {
		t.Fatalf("unexpected response body %q", recorder.Body.String())
	}
}

type fakeStore struct {
	getCalled    int
	setCalled    int
	getResponse  *proxy.Response
	getErr       error
	setErr       error
	lastKey      string
	lastResponse *proxy.Response
	lastTTL      time.Duration
}

func (f *fakeStore) Get(_ context.Context, key string) (*proxy.Response, error) {
	f.getCalled++
	f.lastKey = key
	return f.getResponse, f.getErr
}

func (f *fakeStore) Set(_ context.Context, key string, response *proxy.Response, ttl time.Duration) error {
	f.setCalled++
	f.lastKey = key
	f.lastResponse = response
	f.lastTTL = ttl
	return f.setErr
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
