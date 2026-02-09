package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/robertomachorro/doormanlb/internal/config"
	"github.com/robertomachorro/doormanlb/internal/service"
)

type Handler struct {
	service service.RequestService
}

func NewHandler(service service.RequestService) *Handler {
	return &Handler{service: service}
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL == nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}

	switch request.URL.Path {
	case config.AdminPathPrefix + "health":
		h.handleHealth(writer)
		return
	case config.AdminPathPrefix + "ready":
		h.handleReady(writer, request)
		return
	case config.AdminPathPrefix + "metrics":
		h.handleMetrics(writer)
		return
	}

	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.service.Handle(request.Context(), request, writer); err != nil {
		log.Printf("request failed: %v", err)

		statusCode := http.StatusBadGateway
		if errors.Is(err, errBadRequest) {
			statusCode = http.StatusBadRequest
		}
		http.Error(writer, fmt.Sprintf("upstream routing failed: %v", err), statusCode)
	}
}

var errBadRequest = errors.New("bad request")

func (h *Handler) handleHealth(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte("ok"))
}

func (h *Handler) handleReady(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()

	if err := h.service.Ready(ctx); err != nil {
		http.Error(writer, fmt.Sprintf("not ready: %v", err), http.StatusServiceUnavailable)
		return
	}

	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte("ready"))
}

func (h *Handler) handleMetrics(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(h.service.Metrics()); err != nil {
		http.Error(writer, "failed to write metrics", http.StatusInternalServerError)
	}
}
