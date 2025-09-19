package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
)

func (h *HTTPServer) handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	var cfg config.Config

	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		h.logger.Error("Failed to decode config", zap.Error(err))
		return
	}

	if err := cfg.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("Invalid configuration: %v", err), http.StatusBadRequest)
		h.logger.Error("Invalid configuration", zap.Error(err))
		return
	}

	select {
	case h.configSendChannel <- &cfg:
	case <-time.After(5 * time.Second):
		http.Error(w, "Server is busy processing another configuration", http.StatusServiceUnavailable)
		h.logger.Warn("Configuration update rejected - server busy")
		return
	}

	response := map[string]any{
		"queued": true,
		"hash":   cfg.Hash(),
	}

	h.logger.Info("Configuration update request",
		zap.Bool("queued", true),
		zap.String("hash", cfg.Hash()),
	)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("failed to encode response", zap.Error(err))
	}
}

func (h *HTTPServer) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	cfg := h.getConfig()
	if cfg == nil {
		http.Error(w, "No configuration set", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cfg); err != nil {
		http.Error(w, "Error encoding configuration", http.StatusInternalServerError)
		h.logger.Error("failed to encode response", zap.Error(err))
	}
}

func (h *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"time":   time.Now().Format(time.RFC3339),
	}); err != nil {
		http.Error(w, "Error encoding health status", http.StatusInternalServerError)
		h.logger.Error("failed to encode response", zap.Error(err))
	}
}

func (h *HTTPServer) handleStatsHistory(w http.ResponseWriter, r *http.Request) {
	collector := h.getCollector()
	if collector == nil {
		http.Error(w, "Stats collector not available", http.StatusServiceUnavailable)
		return
	}

	history := collector.GetHistory()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(history); err != nil {
		http.Error(w, "Error encoding stats history", http.StatusInternalServerError)
		h.logger.Error("failed to encode stats history response", zap.Error(err))
	}
}
