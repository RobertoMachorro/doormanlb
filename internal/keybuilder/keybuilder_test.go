package keybuilder

import (
	"net/http"
	"testing"
)

func TestBuildNormalizesQueryOrder(t *testing.T) {
	reqA, _ := http.NewRequest(http.MethodGet, "http://localhost/articles?b=2&a=1", nil)
	reqB, _ := http.NewRequest(http.MethodGet, "http://localhost/articles?a=1&b=2", nil)

	keyA := Build(reqA, Options{})
	keyB := Build(reqB, Options{})

	if keyA == "" || keyB == "" {
		t.Fatal("keys should not be empty")
	}
	if keyA != keyB {
		t.Fatalf("expected normalized keys to match; keyA=%s keyB=%s", keyA, keyB)
	}
}

func TestBuildIgnoresParametersWhenConfigured(t *testing.T) {
	reqA, _ := http.NewRequest(http.MethodGet, "http://localhost/articles?a=1", nil)
	reqB, _ := http.NewRequest(http.MethodGet, "http://localhost/articles?a=99", nil)

	keyA := Build(reqA, Options{IgnoreParameters: true})
	keyB := Build(reqB, Options{IgnoreParameters: true})

	if keyA != keyB {
		t.Fatalf("expected identical keys when parameters are ignored; keyA=%s keyB=%s", keyA, keyB)
	}
}
