package cache

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/robertomachorro/doormanlb/internal/proxy"
)

func TestRedisStoreSetGetAndExpire(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	key := uniqueKey("set-get")

	response := &proxy.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/plain"}},
		Body:       []byte("hello"),
	}

	if err := store.Set(ctx, key, response, 120*time.Millisecond); err != nil {
		t.Fatalf("set response: %v", err)
	}

	cached, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("get response: %v", err)
	}
	if cached == nil {
		t.Fatal("expected cached response")
	}
	if string(cached.Body) != "hello" {
		t.Fatalf("unexpected cached body %q", string(cached.Body))
	}

	time.Sleep(180 * time.Millisecond)
	cached, err = store.Get(ctx, key)
	if err != nil {
		t.Fatalf("get expired response: %v", err)
	}
	if cached != nil {
		t.Fatal("expected response to expire")
	}
}

func TestRedisStoreLeaderLockLifecycle(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	key := uniqueKey("lock")

	lock1, acquired, err := store.TryAcquireLeader(ctx, key, 5*time.Second)
	if err != nil {
		t.Fatalf("acquire leader lock: %v", err)
	}
	if !acquired {
		t.Fatal("expected first lock acquisition to succeed")
	}

	_, acquired, err = store.TryAcquireLeader(ctx, key, 5*time.Second)
	if err != nil {
		t.Fatalf("acquire second leader lock: %v", err)
	}
	if acquired {
		t.Fatal("expected second lock acquisition to fail while lock is held")
	}

	if err := store.ReleaseLeader(ctx, lock1); err != nil {
		t.Fatalf("release leader lock: %v", err)
	}

	_, acquired, err = store.TryAcquireLeader(ctx, key, 5*time.Second)
	if err != nil {
		t.Fatalf("reacquire leader lock: %v", err)
	}
	if !acquired {
		t.Fatal("expected lock acquisition after release")
	}
}

func TestRedisStoreWaitForDoneNotification(t *testing.T) {
	store := newIntegrationStore(t)
	ctx := context.Background()
	key := uniqueKey("wait")

	errCh := make(chan error, 1)
	go func() {
		errCh <- store.WaitForDone(ctx, key, 2*time.Second)
	}()

	time.Sleep(50 * time.Millisecond)
	if err := store.PublishDone(ctx, key); err != nil {
		t.Fatalf("publish done: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("wait for done returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait for done timed out")
	}
}

func newIntegrationStore(t *testing.T) *RedisStore {
	t.Helper()
	redisURL := os.Getenv("REDIS_URL_TEST")
	if redisURL == "" {
		t.Skip("REDIS_URL_TEST is not set; skipping Redis integration tests")
	}

	store, err := NewRedisStore(redisURL)
	if err != nil {
		t.Fatalf("new redis store: %v", err)
	}
	return store
}

func uniqueKey(prefix string) string {
	return fmt.Sprintf("itest:%s:%d", prefix, time.Now().UnixNano())
}
