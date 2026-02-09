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

func boolPtr(value bool) *bool {
	return &value
}
