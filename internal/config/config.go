package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	StrategyRoundRobin       = "ROUND_ROBIN"
	StrategyLeastConnections = "LEAST_CONNECTIONS"

	CacheBehaviorCache       = "CACHE"
	CacheBehaviorPassthrough = "PASSTHROUGH"

	DefaultEndpointKey = "DEFAULT"
	AdminPathPrefix    = "/__doormanlb/"
)

type Config struct {
	Services  []string                  `json:"services"`
	Strategy  string                    `json:"strategy"`
	Endpoints map[string]EndpointConfig `json:"endpoints"`
}

type EndpointConfig struct {
	ExpireTimeout    int64  `json:"expireTimeout,omitempty"`
	CacheBehavior    string `json:"cacheBehavior,omitempty"`
	IgnoreParameters *bool  `json:"ignoreParameters,omitempty"`
}

func Load(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file %q: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(contents, &cfg); err != nil {
		return Config{}, fmt.Errorf("decoding config file %q: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if len(c.Services) == 0 {
		return errors.New("services must contain at least one upstream")
	}

	for i, serviceURL := range c.Services {
		if strings.TrimSpace(serviceURL) == "" {
			return fmt.Errorf("services[%d] cannot be empty", i)
		}
	}

	if c.Strategy == "" {
		return errors.New("strategy is required")
	}

	switch c.Strategy {
	case StrategyRoundRobin, StrategyLeastConnections:
	default:
		return fmt.Errorf("unsupported strategy %q", c.Strategy)
	}

	if c.Endpoints == nil {
		return errors.New("endpoints are required")
	}

	defaultEndpoint, ok := c.Endpoints[DefaultEndpointKey]
	if !ok {
		return fmt.Errorf("endpoints.%s is required", DefaultEndpointKey)
	}

	if err := validateEndpoint(defaultEndpoint, true); err != nil {
		return fmt.Errorf("invalid endpoints.%s: %w", DefaultEndpointKey, err)
	}

	for endpoint, endpointCfg := range c.Endpoints {
		if endpoint == DefaultEndpointKey {
			continue
		}
		if endpoint == "" {
			return errors.New("endpoint keys cannot be empty")
		}
		if strings.HasPrefix(endpoint, AdminPathPrefix) {
			return fmt.Errorf("endpoint key %q uses reserved prefix %q", endpoint, AdminPathPrefix)
		}
		if err := validateEndpoint(endpointCfg, false); err != nil {
			return fmt.Errorf("invalid endpoints.%s: %w", endpoint, err)
		}
	}

	return nil
}

func validateEndpoint(endpointCfg EndpointConfig, requireBehavior bool) error {
	if endpointCfg.ExpireTimeout < 0 {
		return errors.New("expireTimeout must be >= 0")
	}

	if endpointCfg.CacheBehavior != "" {
		switch endpointCfg.CacheBehavior {
		case CacheBehaviorCache, CacheBehaviorPassthrough:
		default:
			return fmt.Errorf("unsupported cacheBehavior %q", endpointCfg.CacheBehavior)
		}
	} else if requireBehavior {
		return errors.New("cacheBehavior is required")
	}

	return nil
}

func (c Config) Endpoint(path string) EndpointConfig {
	defaultCfg := c.Endpoints[DefaultEndpointKey]
	override, ok := c.Endpoints[path]
	if !ok {
		return defaultCfg
	}

	merged := defaultCfg
	if override.ExpireTimeout > 0 {
		merged.ExpireTimeout = override.ExpireTimeout
	}
	if override.CacheBehavior != "" {
		merged.CacheBehavior = override.CacheBehavior
	}
	if override.IgnoreParameters != nil {
		merged.IgnoreParameters = override.IgnoreParameters
	}

	return merged
}

func (c Config) UsesCache() bool {
	defaultCfg := c.Endpoints[DefaultEndpointKey]
	if defaultCfg.CacheBehavior == CacheBehaviorCache {
		return true
	}

	for endpoint, endpointCfg := range c.Endpoints {
		if endpoint == DefaultEndpointKey {
			continue
		}
		if endpointCfg.CacheBehavior == CacheBehaviorCache {
			return true
		}
	}

	return false
}

func (e EndpointConfig) ShouldIgnoreParameters() bool {
	return e.IgnoreParameters != nil && *e.IgnoreParameters
}

func (e EndpointConfig) CacheTTL() time.Duration {
	return time.Duration(e.ExpireTimeout) * time.Millisecond
}
