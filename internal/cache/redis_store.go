package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/robertomachorro/doormanlb/internal/proxy"
)

const (
	responsePrefix = "resp:"
	lockPrefix     = "lock:"
	donePrefix     = "done:"
)

var ErrWaitTimeout = errors.New("wait timeout")

type Store interface {
	Get(ctx context.Context, key string) (*proxy.Response, error)
	Set(ctx context.Context, key string, response *proxy.Response, ttl time.Duration) error
	TryAcquireLeader(ctx context.Context, key string, ttl time.Duration) (*Lock, bool, error)
	ReleaseLeader(ctx context.Context, lock *Lock) error
	PublishDone(ctx context.Context, key string) error
	WaitForDone(ctx context.Context, key string, timeout time.Duration) error
}

type RedisStore struct {
	client *redis.Client
}

type Lock struct {
	Key   string
	Token string
}

type cachedResponse struct {
	StatusCode int                 `json:"statusCode"`
	Header     map[string][]string `json:"header"`
	Body       []byte              `json:"body"`
}

func NewRedisStore(redisURL string) (*RedisStore, error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(options)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &RedisStore{client: client}, nil
}

func (s *RedisStore) Get(ctx context.Context, key string) (*proxy.Response, error) {
	value, err := s.client.Get(ctx, responsePrefix+key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("get cached response: %w", err)
	}

	var cached cachedResponse
	if err := json.Unmarshal([]byte(value), &cached); err != nil {
		return nil, fmt.Errorf("decode cached response: %w", err)
	}

	return &proxy.Response{
		StatusCode: cached.StatusCode,
		Header:     cached.Header,
		Body:       append([]byte(nil), cached.Body...),
	}, nil
}

func (s *RedisStore) Set(ctx context.Context, key string, response *proxy.Response, ttl time.Duration) error {
	if response == nil {
		return errors.New("response cannot be nil")
	}

	cached := cachedResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       response.Body,
	}

	serialized, err := json.Marshal(cached)
	if err != nil {
		return fmt.Errorf("encode cached response: %w", err)
	}

	if err := s.client.Set(ctx, responsePrefix+key, serialized, ttl).Err(); err != nil {
		return fmt.Errorf("set cached response: %w", err)
	}

	return nil
}

func (s *RedisStore) TryAcquireLeader(ctx context.Context, key string, ttl time.Duration) (*Lock, bool, error) {
	if ttl <= 0 {
		ttl = 15 * time.Second
	}

	token, err := randomToken()
	if err != nil {
		return nil, false, fmt.Errorf("generate lock token: %w", err)
	}

	lockKey := lockPrefix + key
	acquired, err := s.client.SetNX(ctx, lockKey, token, ttl).Result()
	if err != nil {
		return nil, false, fmt.Errorf("acquire leader lock: %w", err)
	}
	if !acquired {
		return nil, false, nil
	}

	return &Lock{Key: key, Token: token}, true, nil
}

func (s *RedisStore) ReleaseLeader(ctx context.Context, lock *Lock) error {
	if lock == nil {
		return nil
	}

	const script = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`
	if err := s.client.Eval(ctx, script, []string{lockPrefix + lock.Key}, lock.Token).Err(); err != nil {
		return fmt.Errorf("release leader lock: %w", err)
	}

	return nil
}

func (s *RedisStore) PublishDone(ctx context.Context, key string) error {
	if err := s.client.Publish(ctx, donePrefix+key, "done").Err(); err != nil {
		return fmt.Errorf("publish done notification: %w", err)
	}
	return nil
}

func (s *RedisStore) WaitForDone(ctx context.Context, key string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	pubsub := s.client.Subscribe(ctx, donePrefix+key)
	defer pubsub.Close()

	if _, err := pubsub.Receive(ctx); err != nil {
		return fmt.Errorf("subscribe done notification: %w", err)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-pubsub.Channel():
		return nil
	case <-timer.C:
		return ErrWaitTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

func randomToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func (s *RedisStore) Ping(ctx context.Context) error {
	if err := s.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	return nil
}
