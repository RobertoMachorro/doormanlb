package routing

import "testing"

func TestRoundRobinOrder(t *testing.T) {
	router, err := NewRouter([]string{"http://svc-a", "http://svc-b"}, "ROUND_ROBIN")
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	leaseA := router.Acquire()
	leaseB := router.Acquire()
	leaseC := router.Acquire()
	defer leaseA.Release()
	defer leaseB.Release()
	defer leaseC.Release()

	if leaseA.URL != "http://svc-a" {
		t.Fatalf("expected first lease to route to svc-a, got %s", leaseA.URL)
	}
	if leaseB.URL != "http://svc-b" {
		t.Fatalf("expected second lease to route to svc-b, got %s", leaseB.URL)
	}
	if leaseC.URL != "http://svc-a" {
		t.Fatalf("expected third lease to route to svc-a, got %s", leaseC.URL)
	}
}

func TestLeastConnectionsPrefersLessBusyNode(t *testing.T) {
	router, err := NewRouter([]string{"http://svc-a", "http://svc-b"}, "LEAST_CONNECTIONS")
	if err != nil {
		t.Fatalf("creating router: %v", err)
	}

	lease1 := router.Acquire()
	lease2 := router.Acquire()

	if lease1.URL != "http://svc-a" {
		t.Fatalf("expected first lease to route to svc-a, got %s", lease1.URL)
	}
	if lease2.URL != "http://svc-b" {
		t.Fatalf("expected second lease to route to svc-b while svc-a is busy, got %s", lease2.URL)
	}

	lease1.Release()
	lease2.Release()
}
