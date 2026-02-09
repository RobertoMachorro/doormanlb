package routing

import (
	"errors"
	"fmt"
	"sync/atomic"
)

type Router struct {
	strategy string
	nodes    []*node
	next     uint64
}

type node struct {
	url      string
	inflight int64
}

func NewRouter(services []string, strategy string) (*Router, error) {
	if len(services) == 0 {
		return nil, errors.New("at least one service is required")
	}

	nodes := make([]*node, 0, len(services))
	for _, serviceURL := range services {
		nodes = append(nodes, &node{url: serviceURL})
	}

	switch strategy {
	case "ROUND_ROBIN", "LEAST_CONNECTIONS":
	default:
		return nil, fmt.Errorf("unsupported strategy %q", strategy)
	}

	return &Router{strategy: strategy, nodes: nodes}, nil
}

func (r *Router) Acquire() *Lease {
	n := r.selectNode()
	atomic.AddInt64(&n.inflight, 1)
	return &Lease{URL: n.url, releaseFn: func() { atomic.AddInt64(&n.inflight, -1) }}
}

func (r *Router) selectNode() *node {
	if r.strategy == "ROUND_ROBIN" {
		index := atomic.AddUint64(&r.next, 1)
		return r.nodes[(index-1)%uint64(len(r.nodes))]
	}

	selected := r.nodes[0]
	selectedLoad := atomic.LoadInt64(&selected.inflight)
	for i := 1; i < len(r.nodes); i++ {
		current := r.nodes[i]
		currentLoad := atomic.LoadInt64(&current.inflight)
		if currentLoad < selectedLoad {
			selected = current
			selectedLoad = currentLoad
		}
	}

	return selected
}

type Lease struct {
	URL       string
	released  atomic.Bool
	releaseFn func()
}

func (l *Lease) Release() {
	if l == nil {
		return
	}
	if l.released.CompareAndSwap(false, true) {
		l.releaseFn()
	}
}
