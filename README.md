## [WIP]: *⚠️ MOSTLY WRITTEN WITH CLAUDE OPUS 4 ⚠️*

# NATS Load Tester

## Overview
A dynamically configurable load testing tool for NATS messaging systems with real-time configuration updates via HTTP API.

## Key Features
- **Dynamic Configuration**: Update test parameters on-the-fly via RESTful HTTP API without restarting
- **Comprehensive Load Testing**: Test various scenarios including high stream counts, different replica configurations, and slow consumers
- **Latency Measurement**: Track end-to-end message latency with statistical analysis (min, max, mean, 99th percentile)
- **Persistent Statistics**: Store detailed performance metrics to BadgerDB or file storage
- **Configuration-Based Reporting**: Group statistics by test configuration with rolling history (last 10 snapshots)
- **Failure Detection**: Monitor and log NATS failures with contextual information
- **Kubernetes Ready**: Deploy as containerized application with Kubernetes manifests

## Architecture
1. **Control Plane**: HTTP API server (Chi framework) for configuration management
2. **Load Generation Engine**: Core component for NATS interaction (streams, publishers, consumers)
3. **Stats Collector**: Periodic metrics collection with configurable storage backends

## Technology Stack
- **Language**: Go 1.25.1
- **Web Framework**: Chi (github.com/go-chi/chi)
- **CLI**: Cobra (github.com/spf13/cobra)
- **NATS Client**: Official NATS Go client
- **Storage**: BadgerDB for structured stats, file for simple logging
- **Container**: Docker with Kubernetes deployment

## Build & Deploy
```bash
make build    # Build Docker image
make push     # Build and push to registry
make deploy   # Full deployment to Kubernetes
make clean    # Remove from Kubernetes cluster
```
