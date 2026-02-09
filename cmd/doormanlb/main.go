package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/robertomachorro/doormanlb/internal/cache"
	conf "github.com/robertomachorro/doormanlb/internal/config"
	httpHandler "github.com/robertomachorro/doormanlb/internal/http"
	"github.com/robertomachorro/doormanlb/internal/proxy"
	"github.com/robertomachorro/doormanlb/internal/routing"
	"github.com/robertomachorro/doormanlb/internal/service"
)

const (
	defaultPort       = "8080"
	defaultConfigPath = "config.json"
	defaultRedisURL   = "redis://127.0.0.1:6379"
	shutdownTimeout   = 10 * time.Second
	readHeaderTimeout = 10 * time.Second
)

func main() {
	port := envOrDefault("PORT", defaultPort)
	configPath := envOrDefault("CONFIG_PATH", defaultConfigPath)

	cfg, err := conf.Load(configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	router, err := routing.NewRouter(cfg.Services, cfg.Strategy)
	if err != nil {
		log.Fatalf("creating router: %v", err)
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" && cfg.UsesCache() {
		redisURL = defaultRedisURL
	}

	var cacheStore cache.Store
	if redisURL != "" {
		cacheStore, err = cache.NewRedisStore(redisURL)
		if err != nil {
			log.Fatalf("initializing redis store: %v", err)
		}
	}

	proxyClient := proxy.NewClient()
	svc := service.NewCachingService(cfg, router, cacheStore, proxyClient)
	h := httpHandler.NewHandler(svc)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%s", port),
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	go func() {
		log.Printf("doormanlb listening on :%s", port)
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Fatalf("http server error: %v", serveErr)
		}
	}()

	shutdown(server)
}

func shutdown(server *http.Server) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
