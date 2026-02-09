package config

import "testing"

func TestValidateRequiresDefaultEndpoint(t *testing.T) {
	cfg := Config{
		Services: []string{"http://svc-a:8080"},
		Strategy: StrategyRoundRobin,
		Endpoints: map[string]EndpointConfig{
			"/": {CacheBehavior: CacheBehaviorPassthrough},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected an error when DEFAULT endpoint is missing")
	}
}

func TestEndpointMergesDefaultWithOverride(t *testing.T) {
	ignoreParameters := true

	cfg := Config{
		Services: []string{"http://svc-a:8080"},
		Strategy: StrategyLeastConnections,
		Endpoints: map[string]EndpointConfig{
			DefaultEndpointKey: {
				ExpireTimeout:    600_000,
				CacheBehavior:    CacheBehaviorCache,
				IgnoreParameters: boolPtr(false),
			},
			"/health": {
				CacheBehavior:    CacheBehaviorPassthrough,
				IgnoreParameters: &ignoreParameters,
			},
		},
	}

	endpoint := cfg.Endpoint("/health")
	if endpoint.ExpireTimeout != 600_000 {
		t.Fatalf("expected default expireTimeout, got %d", endpoint.ExpireTimeout)
	}
	if endpoint.CacheBehavior != CacheBehaviorPassthrough {
		t.Fatalf("expected override cacheBehavior PASSTHROUGH, got %s", endpoint.CacheBehavior)
	}
	if endpoint.IgnoreParameters == nil || !*endpoint.IgnoreParameters {
		t.Fatal("expected override ignoreParameters=true")
	}
}

func TestUsesCache(t *testing.T) {
	cfg := Config{
		Services: []string{"http://svc-a:8080"},
		Strategy: StrategyRoundRobin,
		Endpoints: map[string]EndpointConfig{
			DefaultEndpointKey: {
				CacheBehavior: CacheBehaviorPassthrough,
			},
			"/cached": {
				CacheBehavior: CacheBehaviorCache,
			},
		},
	}

	if !cfg.UsesCache() {
		t.Fatal("expected UsesCache to be true")
	}
}

func TestValidateRejectsReservedAdminPrefix(t *testing.T) {
	cfg := Config{
		Services: []string{"http://svc-a:8080"},
		Strategy: StrategyRoundRobin,
		Endpoints: map[string]EndpointConfig{
			DefaultEndpointKey: {
				CacheBehavior: CacheBehaviorPassthrough,
			},
			AdminPathPrefix + "metrics": {
				CacheBehavior: CacheBehaviorPassthrough,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected reserved admin prefix validation error")
	}
}

func TestValidateRequiresPositiveTTLWhenCachingDefault(t *testing.T) {
	cfg := Config{
		Services: []string{"http://svc-a:8080"},
		Strategy: StrategyRoundRobin,
		Endpoints: map[string]EndpointConfig{
			DefaultEndpointKey: {
				CacheBehavior: CacheBehaviorCache,
				ExpireTimeout: 0,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for CACHE default with zero expireTimeout")
	}
}

func TestValidateRequiresPositiveResolvedTTLForOverride(t *testing.T) {
	cfg := Config{
		Services: []string{"http://svc-a:8080"},
		Strategy: StrategyRoundRobin,
		Endpoints: map[string]EndpointConfig{
			DefaultEndpointKey: {
				CacheBehavior: CacheBehaviorPassthrough,
				ExpireTimeout: 0,
			},
			"/cached": {
				CacheBehavior: CacheBehaviorCache,
				ExpireTimeout: 0,
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for CACHE endpoint with zero resolved expireTimeout")
	}
}

func boolPtr(value bool) *bool {
	return &value
}
