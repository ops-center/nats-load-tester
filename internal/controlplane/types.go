package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.bytebuilders.dev/nats-load-tester/internal/config"
	"go.bytebuilders.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
)

type HTTPServer struct {
	mu                sync.RWMutex
	currentConfig     *config.Config
	configHash        string
	configSendChannel chan<- *config.Config
	collector         *stats.Collector
	server            *http.Server
	logger            *zap.Logger
	port              int
}

func NewHTTPServer(port int, logger *zap.Logger, configSendChannel chan<- *config.Config) *HTTPServer {
	return &HTTPServer{
		port:              port,
		logger:            logger,
		configSendChannel: configSendChannel,
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

func (h *HTTPServer) GetConfig() *config.Config {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.currentConfig
}

func (h *HTTPServer) SetConfig(cfg *config.Config) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.currentConfig = cfg
	h.configHash = cfg.Hash()
}

func (h *HTTPServer) SetCollector(collector *stats.Collector) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.collector = collector
}

func (h *HTTPServer) GetCollector() *stats.Collector {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.collector
}
