# doormanlb
Provides serialized, single-flight request routing to services, eliminating duplicate processing under load.

## Purpose

Load Balancers tend to direct traffic to registered services, if the same endpoint is called multiple times, they will simply forward the request multiple times, creating unnecessary load. Especialy in read-only publishing sites (such as WordPress). By serializing identical requests, the burden of rendering a page or results is done only once.

## Identifying Requests

The cache identifying key is produced by taking the URL/Path and query parameters and producing a hash key. For simplicity, only the GET HTTP method is supported, as other methods expect dynamic interactions.

## Setup

### Environment Variables

```
PORT=8080
REDIS_URL="redis://127.0.0.1:6379"
```

`REDIS_URL` is required when any endpoint uses `cacheBehavior: "CACHE"`.

## Operational Endpoints

- `GET /__doormanlb/health` returns `200 OK` when the process is running.
- `GET /__doormanlb/ready` returns `200 OK` when dependencies are reachable (for cache-enabled configs, this checks Redis).
- `GET /__doormanlb/metrics` returns JSON counters for requests, cache hits/misses, lock waits, and upstream fetches.
- The `"/__doormanlb/"` prefix is reserved and cannot be used as a proxied endpoint key in `config.json`.

### Configuration File

*Services* are the targetted (proxied) locations to direct requests to (typically one service entry in Kubernetes). The *strategy* is how the requests are distributed among the services (LEAST_CONNECTIONS or ROUND_ROBIN for now). Endpoints keeps the configuration for all *endpoints*, the special DEFAULT endpoint is the baseline for all other endpoints. The expiration of requests (*expireTimeout* in milliseconds) and what to do with the endpoint (*cacheBehavior* is either CACHE or PASSTHROUGH). The DEFAULT endpoint is special and applies to all endpoints. Individual endpoints can override the behavior by being listed specifically as an entry to *endpoints*. The configuration options are the same as for DEFAULT, but only apply to the named endpoint. For cached endpoints, upstream `5xx` responses are not stored.

```json
{
  "services": [
    "http://servicename.namespaced.svc.local:80",
    "https://example.com"
  ],
  "strategy": "LEAST_CONNECTIONS",
  "endpoints": {
    "DEFAULT": {
        "expireTimeout": 600_000,
        "cacheBehavior": "CACHE",
        "ignoreParameters": false
    },
    "/": {
      "expireTimeout": 60_000
    },
    "/health": {
      "cacheBehavior": "PASSTHROUGH"
    },
  }
}
```
