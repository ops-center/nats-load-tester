package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.uber.org/zap"
)

type HTTPServer struct {
	*BaseServer
	server *http.Server
	logger *zap.Logger
	port   int
}

func NewHTTPServer(port int, logger *zap.Logger) *HTTPServer {
	return &HTTPServer{
		BaseServer: NewBaseServer(),
		port:       port,
		logger:     logger,
	}
}

func (h *HTTPServer) Start(ctx context.Context) error {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	r.Post("/config", h.handleConfigUpdate)
	r.Get("/config", h.handleConfigGet)
	r.Get("/health", h.handleHealth)
	r.Get("/stats/history", h.handleStatsHistory)

	h.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", h.port),
		Handler: r,
	}

	h.logger.Info("Starting HTTP server", zap.Int("port", h.port))

	go func() {
		<-ctx.Done()
		if err := h.Stop(); err != nil {
			h.logger.Error("failed to stop HTTP server", zap.Error(err))
		}
	}()

	return h.server.ListenAndServe()
}

func (h *HTTPServer) Stop() error {
	if h.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return h.server.Shutdown(ctx)
}

func (h *HTTPServer) handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	var cfg config.Config

	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		h.logger.Error("Failed to decode config", zap.Error(err))
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := cfg.Validate(); err != nil {
		h.logger.Error("Invalid configuration", zap.Error(err))
		http.Error(w, fmt.Sprintf("Invalid configuration: %v", err), http.StatusBadRequest)
		return
	}

	updated := h.SetConfig(cfg)

	response := map[string]any{
		"updated": updated,
		"hash":    cfg.Hash(),
	}

	if !updated {
		response["message"] = "Configuration unchanged"
	}

	h.logger.Info("Configuration update request",
		zap.Bool("updated", updated),
		zap.String("hash", cfg.Hash()),
	)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.logger.Error("failed to encode response", zap.Error(err))
	}
}

func (h *HTTPServer) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	cfg := h.GetConfig()
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
	collector := h.GetCollector()
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
