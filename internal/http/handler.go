package http

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/robertomachorro/doormanlb/internal/service"
)

type Handler struct {
	service service.RequestService
}

func NewHandler(service service.RequestService) *Handler {
	return &Handler{service: service}
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if request.URL == nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
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
