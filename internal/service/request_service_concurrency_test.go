package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robertomachorro/doormanlb/internal/cache"
	"github.com/robertomachorro/doormanlb/internal/config"
	"github.com/robertomachorro/doormanlb/internal/proxy"
	"github.com/robertomachorro/doormanlb/internal/routing"
)

func TestConcurrentIdenticalRequestsSingleFlight(t *testing.T) {
	cfg := config.Config{
		Services: []string{"http://svc-a"},
		Strategy: config.StrategyRoundRobin,
		Endpoints: map[string]config.EndpointConfig{
			config.DefaultEndpointKey: {
				CacheBehavior: config.CacheBehaviorCache,
				ExpireTimeout: 30_000,
			},
		},
	}

	router, err := routing.NewRouter(cfg.Services, cfg.Strategy)
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	store := newMemoryStore()
	fetcher := &countingFetcher{
		response: &proxy.Response{StatusCode: http.StatusOK, Body: []byte("shared")},
		delay:    20 * time.Millisecond,
	}
	svc := NewCachingService(cfg, router, store, fetcher)

	const concurrency = 30
	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://localhost/articles?id=123", nil)
			rec := httptest.NewRecorder()
			if err := svc.Handle(context.Background(), req, rec); err != nil {
				errCh <- err
				return
			}
			if rec.Code != http.StatusOK {
				errCh <- &statusError{code: rec.Code}
				return
			}
			if rec.Body.String() != "shared" {
				errCh <- &bodyError{body: rec.Body.String()}
			}
		}()
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("request failed: %v", err)
	}

	if fetcher.count.Load() != 1 {
		t.Fatalf("expected exactly one upstream fetch, got %d", fetcher.count.Load())
	}
}

func TestConcurrentDifferentKeysFetchIndependently(t *testing.T) {
	cfg := config.Config{
		Services: []string{"http://svc-a"},
		Strategy: config.StrategyRoundRobin,
		Endpoints: map[string]config.EndpointConfig{
			config.DefaultEndpointKey: {
				CacheBehavior: config.CacheBehaviorCache,
				ExpireTimeout: 30_000,
			},
		},
	}

	router, err := routing.NewRouter(cfg.Services, cfg.Strategy)
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	store := newMemoryStore()
	fetcher := &countingFetcher{
		delay: 10 * time.Millisecond,
		responseFn: func(r *http.Request) *proxy.Response {
			id := r.URL.Query().Get("id")
			return &proxy.Response{StatusCode: http.StatusOK, Body: []byte("id=" + id)}
		},
	}
	svc := NewCachingService(cfg, router, store, fetcher)

	runGroup := func(id string) {
		const n = 10
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := httptest.NewRequest(http.MethodGet, "http://localhost/articles?id="+id, nil)
				rec := httptest.NewRecorder()
				if err := svc.Handle(context.Background(), req, rec); err != nil {
					t.Errorf("handle error id=%s: %v", id, err)
					return
				}
				if got := rec.Body.String(); got != "id="+id {
					t.Errorf("unexpected body id=%s got=%q", id, got)
				}
			}()
		}
		wg.Wait()
	}

	runGroup("1")
	runGroup("2")

	if fetcher.count.Load() != 2 {
		t.Fatalf("expected exactly two upstream fetches, got %d", fetcher.count.Load())
	}
}

type countingFetcher struct {
	count      atomic.Uint64
	delay      time.Duration
	response   *proxy.Response
	responseFn func(*http.Request) *proxy.Response
}

func (f *countingFetcher) Fetch(_ context.Context, _ string, request *http.Request) (*proxy.Response, error) {
	f.count.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.responseFn != nil {
		return f.responseFn(request), nil
	}
	return f.response, nil
}

type memoryStore struct {
	mu      sync.Mutex
	values  map[string]*proxy.Response
	locks   map[string]string
	waiters map[string][]chan struct{}
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		values:  make(map[string]*proxy.Response),
		locks:   make(map[string]string),
		waiters: make(map[string][]chan struct{}),
	}
}

func (m *memoryStore) Get(_ context.Context, key string) (*proxy.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	response := m.values[key]
	if response == nil {
		return nil, nil
	}
	return cloneResponse(response), nil
}

func (m *memoryStore) Set(_ context.Context, key string, response *proxy.Response, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = cloneResponse(response)
	return nil
}

func (m *memoryStore) TryAcquireLeader(_ context.Context, key string, _ time.Duration) (*cache.Lock, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.locks[key]; exists {
		return nil, false, nil
	}
	token := strconv.FormatInt(time.Now().UnixNano(), 10)
	m.locks[key] = token
	return &cache.Lock{Key: key, Token: token}, true, nil
}

func (m *memoryStore) ReleaseLeader(_ context.Context, lock *cache.Lock) error {
	if lock == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if token, exists := m.locks[lock.Key]; exists && token == lock.Token {
		delete(m.locks, lock.Key)
	}
	return nil
}

func (m *memoryStore) PublishDone(_ context.Context, key string) error {
	m.mu.Lock()
	waiters := append([]chan struct{}(nil), m.waiters[key]...)
	delete(m.waiters, key)
	m.mu.Unlock()

	for _, waiter := range waiters {
		close(waiter)
	}
	return nil
}

func (m *memoryStore) WaitForDone(ctx context.Context, key string, timeout time.Duration) error {
	waiter := make(chan struct{})
	m.mu.Lock()
	m.waiters[key] = append(m.waiters[key], waiter)
	m.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-waiter:
		return nil
	case <-timer.C:
		return cache.ErrWaitTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *memoryStore) Ping(context.Context) error {
	return nil
}

func cloneResponse(response *proxy.Response) *proxy.Response {
	header := make(http.Header, len(response.Header))
	for key, values := range response.Header {
		header[key] = append([]string(nil), values...)
	}
	return &proxy.Response{
		StatusCode: response.StatusCode,
		Header:     header,
		Body:       append([]byte(nil), response.Body...),
	}
}

type statusError struct {
	code int
}

func (s *statusError) Error() string {
	return "unexpected status " + strconv.Itoa(s.code)
}

type bodyError struct {
	body string
}

func (b *bodyError) Error() string {
	return "unexpected body " + b.body
}
