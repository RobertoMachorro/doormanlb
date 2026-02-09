package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robertomachorro/doormanlb/internal/config"
)

func TestHealthEndpoint(t *testing.T) {
	h := NewHandler(&fakeService{})
	req := httptest.NewRequest(http.MethodGet, "http://localhost"+config.AdminPathPrefix+"health", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("expected ok body, got %q", rec.Body.String())
	}
}

func TestReadyEndpointNotReady(t *testing.T) {
	h := NewHandler(&fakeService{readyErr: errors.New("redis down")})
	req := httptest.NewRequest(http.MethodGet, "http://localhost"+config.AdminPathPrefix+"ready", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	h := NewHandler(&fakeService{metrics: map[string]uint64{"requests_total": 3}})
	req := httptest.NewRequest(http.MethodGet, "http://localhost"+config.AdminPathPrefix+"metrics", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "\"requests_total\":3") {
		t.Fatalf("expected metrics json, got %q", rec.Body.String())
	}
}

func TestNonAdminPathIsProxied(t *testing.T) {
	svc := &fakeService{}
	h := NewHandler(svc)
	req := httptest.NewRequest(http.MethodGet, "http://localhost/health", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if !svc.handleCalled {
		t.Fatal("expected non-admin path to call service handler")
	}
}

type fakeService struct {
	handleErr    error
	readyErr     error
	metrics      map[string]uint64
	handleCalled bool
}

func (f *fakeService) Handle(_ context.Context, _ *http.Request, _ http.ResponseWriter) error {
	f.handleCalled = true
	return f.handleErr
}

func (f *fakeService) Ready(_ context.Context) error {
	return f.readyErr
}

func (f *fakeService) Metrics() map[string]uint64 {
	if f.metrics == nil {
		return map[string]uint64{}
	}
	return f.metrics
}
