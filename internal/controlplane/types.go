/*
Copyright AppsCode Inc. and Contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controlplane

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"runtime"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opscenter.dev/nats-load-tester/internal/config"
	"go.opscenter.dev/nats-load-tester/internal/stats"
	"go.uber.org/zap"
)

const (
	ConfigUpdateThrottleLimit = 10
	RequestTimeoutSeconds     = 60
	MaxHeaderBytes            = 1 << 20
	ReadTimeoutSeconds        = 10
	WriteTimeoutSeconds       = 10
	IdleTimeoutSeconds        = 120
)

type HTTPServer struct {
	mu                sync.RWMutex
	configManager     configManager
	configSendChannel chan<- *config.Config
	collector         *stats.Collector
	server            *http.Server
	logger            *zap.Logger
	port              int
	enablePprof       bool
}

type configManager struct {
	config *config.Config
	hash   string
}

func NewHTTPServer(port int, enablePprof bool, logger *zap.Logger, configSendChannel chan<- *config.Config) *HTTPServer {
	if enablePprof {
		runtime.SetBlockProfileRate(1)
		runtime.SetMutexProfileFraction(1)
		logger.Info("pprof profiling enabled")
	}

	return &HTTPServer{
		port:              port,
		enablePprof:       enablePprof,
		logger:            logger,
		configManager:     configManager{},
		configSendChannel: configSendChannel,
	}
}

func (h *HTTPServer) Start(ctx context.Context) error {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(RequestTimeoutSeconds * time.Second))

	// Healthcheck endpoint without logging
	r.Get("/healthcheck", h.handleCheckHealth)

	r.Group(func(r chi.Router) {
		r.Use(middleware.Logger)

		r.Group(func(r chi.Router) {
			r.Use(middleware.Throttle(ConfigUpdateThrottleLimit))
			r.Post("/config", h.handleConfigUpdate)
		})

		r.Get("/config", h.handleConfigGet)
		r.Get("/stats", h.handleGetStatsHistory)
	})

	if h.enablePprof {
		r.HandleFunc("/debug/pprof/", pprof.Index)
		r.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		r.HandleFunc("/debug/pprof/profile", pprof.Profile)
		r.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		r.HandleFunc("/debug/pprof/trace", pprof.Trace)
		r.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
		r.Handle("/debug/pprof/heap", pprof.Handler("heap"))
		r.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
		r.Handle("/debug/pprof/block", pprof.Handler("block"))
		r.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
		r.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
		h.logger.Info("pprof endpoints registered", zap.String("path", "/debug/pprof/"))
	}

	h.server = &http.Server{
		Addr:           fmt.Sprintf(":%d", h.port),
		Handler:        r,
		MaxHeaderBytes: MaxHeaderBytes,
		ReadTimeout:    ReadTimeoutSeconds * time.Second,
		WriteTimeout:   WriteTimeoutSeconds * time.Second,
		IdleTimeout:    IdleTimeoutSeconds * time.Second,
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

	ctx, cancel := context.WithTimeout(context.Background(), ServerShutdownTimeout)
	defer cancel()

	return h.server.Shutdown(ctx)
}

func (h *HTTPServer) SetConfig(cfg *config.Config) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.configManager.setConfig(cfg)
}

func (h *HTTPServer) SetCollector(collector *stats.Collector) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.collector = collector
}

func (c *configManager) setConfig(cfg *config.Config) {
	c.config = cfg
	c.hash = cfg.Hash()
}

func (h *HTTPServer) getConfig() *config.Config {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.configManager.config
}

func (h *HTTPServer) getCollector() *stats.Collector {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.collector
}
