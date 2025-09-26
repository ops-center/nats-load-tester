# NATS Load Tester

NATS JetStream load testing tool for Kubernetes clusters.

## Kubernetes Deployment

```bash
# Deploy to cluster
make deploy

```

## Deployment Customization

### Default Configuration
Modify `k8s/configmap.yaml` to set the default load test specifications that pods load on startup.

## API Endpoints

```bash
curl -X POST http://service-endpoint:9481/config \
  -H "Content-Type: application/json" \
  -d @config.default.json
```

```bash
curl http://service-endpoint:9481/stats?limit=10
```

## Configuration

### Environment Variable Support

| Pattern | Replacement | Description |
|---------|-------------|-------------|
| `{}` | Stream number | Dynamic subject generation per stream |

> **Note**: Full `${VAR}` environment variable expansion is not yet supported.

### Configuration Structure

#### Root Configuration

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `load_test_specs` | `[]LoadTestSpec` | ✓ | - | Sequential load test configurations |
| `repeat_policy` | `RepeatPolicy` | - | `{"enabled": false}` | Test repetition with scaling multipliers |
| `storage` | `Storage` | - | `{"type": "badger", "path": "./load_test_stats.db"}` | Statistics storage backend |
| `stats_collection_interval_seconds` | `int64` | - | `5` | Statistics collection interval |

#### LoadTestSpec

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

#### StreamConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name_prefix` | `string` | ✓ | - | Stream name prefix (indexed: stream_1, stream_2...) |
| `count` | `int32` | ✓ | - | Number of streams to create |
| `replicas` | `int32` | ✓ | - | JetStream replication factor |
| `subjects` | `[]string` | ✓ | - | NATS subjects (`{}` = stream index placeholder) |
|`messages_per_stream_per_second` | `int64` | ✓ | - | Target message throughput per stream |
| `retention` | `string` | - | `"limits"` | Retention: `limits`, `interest`, `workqueue` |
| `max_age` | `string` | - | `"1m"` | Message TTL (e.g., `5m`, `2h30m`, `24h`) |
| `storage` | `string` | - | `"memory"` | Storage: `memory`, `file` |
| `discard_new_per_subject` | `*bool` | - | `true` | Discard new messages per subject at limits |
| `discard` | `string` | - | `"old"` | Discard policy: `old`, `new` |
| `max_msgs` | `*int64` | - | `-1` | Max message count (-1 = unlimited) |
| `max_bytes` | `*int64` | - | `-1` | Max storage bytes (-1 = unlimited) |
| `max_msgs_per_subject` | `*int64` | - | `-1` | Max messages per subject (-1 = unlimited) |
| `max_consumers` | `*int` | - | `-1` | Max consumers allowed (-1 = unlimited) |

#### PublisherConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `count_per_stream` | `int32` | ✓ | - | Publishers per stream |
| `stream_name_prefix` | `string` | ✓ | - | Target stream prefix to match |
| `publish_rate_per_second` | `int32` | ✓ | - | Messages/sec per publisher |
| `publish_pattern` | `string` | ✓ | - | Pattern: `steady`, `random` |
| `publish_burst_size` | `int32` | ✓ | - | Burst size for random pattern |
| `message_size_bytes` | `int32` | ✓ | - | Message payload size |
| `track_latency` | `bool` | ✓ | - | Enable end-to-end latency tracking |

#### ConsumerConfig

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

#### BehaviorConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `duration_seconds` | `int64` | ✓ | - | Total test duration |
| `ramp_up_seconds` | `int64` | ✓ | - | Gradual rate increase period |

#### Storage

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `type` | `string` | - | `"badger"` | Storage: `file`, `badger`, `stdout` |
| `path` | `string` | - | `"./load_test_stats.db"` | Storage location |

#### RepeatPolicy

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `enabled` | `bool` | - | `false` | Enable test repetition with scaling |
| `streams.*_multiplier` | `float64` | - | `1.0` | Stream parameter scaling factors |
| `publishers.*_multiplier` | `float64` | - | `1.0` | Publisher parameter scaling factors |
| `consumers.*_multiplier` | `float64` | - | `1.0` | Consumer parameter scaling factors |
| `behavior.*_multiplier` | `float64` | - | `1.0` | Behavior parameter scaling factors |

### Example Configuration

```json
{
  "load_test_specs": [{
    "name": "load_test",
    "nats_url": "${NATS_URL}",
    "nats_creds_file": "${NATS_CREDS_MOUNT_PATH}/admin.creds",
    "use_jetstream": true,
    "client_id_prefix": "load_tester",
    "streams": [{
      "name_prefix": "load_stream",
      "count": 5,
      "replicas": 1,
      "subjects": ["test.{}"],
      "max_msgs": 100000,
      "max_bytes": 104857600,
      "storage": "file"
    }],
    "publishers": {
      "count_per_stream": 10,
      "stream_name_prefix": "load_stream",
      "publish_rate_per_second": 1000,
      "publish_pattern": "steady",
      "message_size_bytes": 1024,
      "track_latency": true
    },
    "consumers": {
      "stream_name_prefix": "load_stream",
      "type": "pull",
      "count_per_stream": 5,
      "durable_name_prefix": "consumer",
      "ack_wait_seconds": 30,
      "max_ack_pending": 1000,
      "ack_policy": "explicit"
    },
    "behavior": {
      "duration_seconds": 300,
      "ramp_up_seconds": 30
    }
  }],
  "storage": {
    "type": "badger",
    "path": "/var/lib/lt/stats.db"
  }
}
```

## TODO

- [x] **CLI Operational Modes**: Add `--mode=publish|consume|both` CLI arguments for specialized pod roles
- [ ] **Synchronize the replicas and distribute the load-generation across each pod**
- [ ] **Unified Service Endpoint**: Create master service that accepts single configuration and forwards to all replicated pods
- [ ] **NATS API Migration**: Update from deprecated JetStream API to newer `github.com/nats-io/nats.go/jetstream`
- [ ] **Enhanced Metrics System**: Implement comprehensive metrics collection including system resources (CPU, memory, goroutines), NATS-specific metrics (connection health, bytes in/out), JetStream performance (storage usage, cluster status), enhanced latency analysis (P50, P90, P95, P99.9, P99.99 percentiles), throughput trends, error categorization, and test progress tracking. Add Prometheus export, real-time WebSocket streaming, and comparative analysis capabilities for production-grade observability.
