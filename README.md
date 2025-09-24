# NATS Load Tester

A high-throughput load testing tool for NATS JetStream with real-time configuration via HTTP API.

Validates NATS cluster performance under realistic workloads with configurable message patterns, consumer types, and stream configurations.

## Usage

### Local Development

```bash
# Run with default config
go run cmd/load-tester/main.go --use-default-config

# Run with custom config file
go run cmd/load-tester/main.go --config-file-path config.default.json

# Run with debug logging
go run cmd/load-tester/main.go --log-level debug --use-default-config

# Run tests
go test ./... -v

# Build Docker image
make build
```

### Kubernetes Deployment

```bash
# Deploy to cluster
make deploy

# Deploy with custom NATS configuration
make deploy NATS_SERVICE_NAME=my-nats NATS_SERVICE_NAMESPACE=nats NATS_PORT=4222
```

### CLI Arguments

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `9481` | HTTP API server port |
| `--log-level` | `info` | Log level: debug, info, warn, error |
| `--config-file-path` | - | Load configuration from file on startup |
| `--use-default-config` | `false` | Load config.default.json on startup |

## API

**Submit load test configuration:**
```bash
curl -X POST http://localhost:9481/config \
  -H "Content-Type: application/json" \
  -d @config.default.json
```

**Get current configuration:**
```bash
curl http://localhost:9481/config
```

**Retrieve statistics (optional limit parameter):**
```bash
curl http://localhost:9481/stats/history?limit=10
```

**Health check:**
```bash
curl http://localhost:9481/healthcheck
```

## Configuration

### Example Default Configuration

```json
{
  "load_test_specs": [{
    "name": "100-streams-3-replicas-immediate",
    "nats_url": "nats://localhost:4222",
    "nats_creds_file": "",
    "use_jetstream": true,
    "client_id_prefix": "load-tester",
    "streams": [{
      "name_prefix": "load_stream",
      "count": 100,
      "replicas": 3,
      "subjects": ["test.subject.{}"],
      "messages_per_stream_per_second": 1000,
      "retention": "limits",
      "max_age": "5m",
      "storage": "memory",
      "discard_new_per_subject": true,
      "discard": "old",
      "max_msgs": 100000,
      "max_bytes": 104857600,
      "max_msgs_per_subject": 10000,
      "max_consumers": 50
    }],
    "publishers": {
      "count_per_stream": 1,
      "stream_name_prefix": "load_stream",
      "publish_rate_per_second": 1000,
      "publish_pattern": "steady",
      "publish_burst_size": 1,
      "message_size_bytes": 1024,
      "track_latency": true
    },
    "consumers": {
      "stream_name_prefix": "load_stream",
      "type": "push",
      "count_per_stream": 1,
      "durable_name_prefix": "load_consumer",
      "ack_wait_seconds": 30,
      "max_ack_pending": 1000,
      "consume_delay_ms": 0,
      "ack_policy": "explicit"
    },
    "behavior": {
      "duration_seconds": 600,
      "ramp_up_seconds": 30
    },
    "log_limits": {
      "max_lines": 200,
      "max_bytes": 65536
    }
  }],
  "repeat_policy": {
    "enabled": true,
    "streams": {
      "count_multiplier": 2,
      "replicas_multiplier": 1,
      "messages_per_stream_per_second_multiplier": 1.5
    },
    "behavior": {
      "duration_multiplier": 1.2,
      "ramp_up_multiplier": 1.2
    },
    "consumers": {
      "ack_wait_multiplier": 1.5,
      "max_ack_pending_multiplier": 1.5,
      "consume_delay_multiplier": 1
    },
    "publishers": {
      "count_per_stream_multiplier": 1,
      "publish_rate_multiplier": 1,
      "message_size_bytes_multiplier": 1
    }
  },
  "storage": {
    "type": "file",
    "path": "/var/lib/lt/load_test_stats.log"
  },
  "stats_collection_interval_seconds": 5
}
```

### Root Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `load_test_specs` | `[]LoadTestSpec` | ✓ | - | Sequential load test configurations |
| `repeat_policy` | `RepeatPolicy` | - | `{"enabled": false}` | Test repetition with scaling multipliers |
| `storage` | `Storage` | - | `{"type": "badger", "path": "./load_test_stats.db"}` | Statistics storage backend |
| `stats_collection_interval_seconds` | `int64` | - | `5` | Statistics collection interval |

### LoadTestSpec

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name` | `string` | ✓ | - | Unique test identifier |
| `nats_url` | `string` | ✓ | - | NATS server connection URL |
| `nats_creds_file` | `string` | - | `""` | NATS credentials file path |
| `use_jetstream` | `bool` | - | `false` | Enable JetStream for persistence |
| `client_id_prefix` | `string` | - | `"load-tester"` | NATS client ID prefix |
| `streams` | `[]StreamConfig` | ✓ | - | JetStream stream definitions |
| `publishers` | `PublisherConfig` | ✓ | - | Message publisher configuration |
| `consumers` | `ConsumerConfig` | ✓ | - | Message consumer configuration |
| `behavior` | `BehaviorConfig` | ✓ | - | Test execution behavior |
| `log_limits` | `LogLimits` | - | - | Logging constraints |

### StreamConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name_prefix` | `string` | ✓ | - | Stream name prefix (indexed: stream_1, stream_2...) |
| `count` | `int32` | ✓ | - | Number of streams to create |
| `replicas` | `int32` | ✓ | - | JetStream replication factor |
| `subjects` | `[]string` | ✓ | - | NATS subjects (`{}` = stream index placeholder) |
| `messages_per_stream_per_second` | `int64` | ✓ | - | Target message throughput per stream |
| `retention` | `string` | - | `"limits"` | Retention: `limits`, `interest`, `workqueue` |
| `max_age` | `string` | - | `"1m"` | Message TTL (e.g., `5m`, `2h30m`, `24h`) |
| `storage` | `string` | - | `"memory"` | Storage: `memory`, `file` |
| `discard_new_per_subject` | `*bool` | - | `true` | Discard new messages per subject at limits |
| `discard` | `string` | - | `"old"` | Discard policy: `old`, `new` |
| `max_msgs` | `*int64` | - | `-1` | Max message count (-1 = unlimited) |
| `max_bytes` | `*int64` | - | `-1` | Max storage bytes (-1 = unlimited) |
| `max_msgs_per_subject` | `*int64` | - | `-1` | Max messages per subject (-1 = unlimited) |
| `max_consumers` | `*int` | - | `-1` | Max consumers allowed (-1 = unlimited) |

**Stream limits:** Control resource usage and test backpressure scenarios. Memory streams need `max_msgs`/`max_bytes` to prevent OOM. Per-subject limits test wildcard subject behavior.

### PublisherConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `count_per_stream` | `int32` | ✓ | - | Publishers per stream |
| `stream_name_prefix` | `string` | ✓ | - | Target stream prefix to match |
| `publish_rate_per_second` | `int32` | ✓ | - | Messages/sec per publisher |
| `publish_pattern` | `string` | ✓ | - | Pattern: `steady`, `random` |
| `publish_burst_size` | `int32` | ✓ | - | Burst size for random pattern |
| `message_size_bytes` | `int32` | ✓ | - | Message payload size |
| `track_latency` | `bool` | ✓ | - | Enable end-to-end latency tracking |

`count_per_stream` scales publishers with streams. `publish_pattern` tests different load characteristics. `track_latency` measures E2E performance but adds overhead.

### ConsumerConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `stream_name_prefix` | `string` | ✓ | - | Source stream prefix to match |
| `type` | `string` | ✓ | - | Consumer type: `push`, `pull` |
| `count_per_stream` | `int32` | ✓ | - | Consumers per stream |
| `durable_name_prefix` | `string` | ✓ | - | Durable consumer name prefix |
| `ack_wait_seconds` | `int64` | ✓ | - | Message acknowledgment timeout |
| `max_ack_pending` | `int32` | ✓ | - | Max unacknowledged messages |
| `consume_delay_ms` | `int64` | - | `0` | Artificial processing delay |
| `ack_policy` | `string` | ✓ | - | Ack policy: `explicit`, `none`, `all` |

### BehaviorConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `duration_seconds` | `int64` | ✓ | - | Total test duration |
| `ramp_up_seconds` | `int64` | ✓ | - | Gradual rate increase period |

Ramp-up prevents connection storms and allows system warm-up for realistic performance testing.

### Storage

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `type` | `string` | - | `"badger"` | Storage: `file`, `badger`, `stdout` |
| `path` | `string` | - | `"./load_test_stats.db"` | Storage location |

**Storage types:**
- `file`: Human-readable logs (default) for debugging
- `badger`: Production embedded DB with TTL and GC
- `stdout`: Container/CI-friendly console output

### RepeatPolicy

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | `bool` | - | `false` | Enable test repetition with scaling |
| `streams.*_multiplier` | `float64` | - | `1.0` | Stream parameter scaling factors |
| `publishers.*_multiplier` | `float64` | - | `1.0` | Publisher parameter scaling factors |
| `consumers.*_multiplier` | `float64` | - | `1.0` | Consumer parameter scaling factors |
| `behavior.*_multiplier` | `float64` | - | `1.0` | Behavior parameter scaling factors |

Automated stress testing by progressively increasing load parameters without manual configuration changes.

## TODO

- [ ] **Enhanced Metrics System**: Implement comprehensive metrics collection including system resources (CPU, memory, goroutines), NATS-specific metrics (connection health, bytes in/out), JetStream performance (storage usage, cluster status), enhanced latency analysis (P50, P90, P95, P99.9, P99.99 percentiles), throughput trends, error categorization, and test progress tracking. Add Prometheus export, real-time WebSocket streaming, and comparative analysis capabilities for production-grade observability.
