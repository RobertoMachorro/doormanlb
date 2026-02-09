package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/robertomachorro/doormanlb/internal/proxy"
)

const responsePrefix = "resp:"

type Store interface {
	Get(ctx context.Context, key string) (*proxy.Response, error)
	Set(ctx context.Context, key string, response *proxy.Response, ttl time.Duration) error
}

type RedisStore struct {
	client *redis.Client
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
