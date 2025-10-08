# NATS Load Tester

NATS JetStream load testing tool for Kubernetes clusters.

## Kubernetes Deployment

### ⚠️TEMPORARY HACK FOR RUNNING ACE-NATS IN CLUSTER-MODE
```bash
#install yq 4.2.0
export VERSION=v4.2.0;
export BINARY=yq_linux_amd64;
wget https://github.com/mikefarah/yq/releases/download/${VERSION}/${BINARY}.tar.gz -O - |\tar xz;
sudo mv ${BINARY} /usr/bin/yq4
```

```bash
sudo chmod +x ./hack/patch-nats-config.sh
./hack/patch-nats-config.sh
# NOTE: the number of stream replicas in the configuration should ideally be equal to the number of ace-nats replicas
kubectl patch sts -n ace ace-nats -p '{"spec":{"replicas":3}}'
```

```bash
export REGISTRY=<your-docker-repo>
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

### Dynamic Placeholders

| Pattern | Replacement | Description |
|---------|-------------|-------------|
| `{}` | Stream number | Dynamic subject/stream name generation per stream (replaced with `1`, `2`, `3`, etc.) |

**Example**: `"subjects": ["test.subject.{}"]` with `"count": 3` creates:
- `load_stream_1` → subject `test.subject.1`
- `load_stream_2` → subject `test.subject.2`
- `load_stream_3` → subject `test.subject.3`

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
| `log_limits` | `LogLimits` | - | `{"max_lines": 1000, "max_bytes": 1048576, "max_latency_samples": 10000}` | Logging and metrics constraints |

#### StreamConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name_prefix` | `string` | ✓ | - | Stream name prefix (indexed: stream_1, stream_2...) |
| `count` | `int32` | ✓ | - | Number of streams to create |
| `replicas` | `int32` | ✓ | - | JetStream replication factor |
| `subjects` | `[]string` | ✓ | - | NATS subjects (`{}` = stream index placeholder) |
|`messages_per_stream_per_second` | `int64` | ✓ | - | Target message throughput per stream (informational, actual rate controlled by publishers) |
| `retention` | `string` | - | `"limits"` | Retention: `limits`, `interest`, `workqueue` |
| `max_age` | `string` | - | `"1m"` | Message TTL (e.g., `5m`, `2h30m`, `24h`) |
| `storage` | `string` | - | `"memory"` | Storage: `memory`, `file` |
| `discard_new_per_subject` | `*bool` | - | `false` | Discard new messages per subject at limits |
| `discard` | `string` | - | `"old"` | Discard policy: `old`, `new` |
| `max_msgs` | `*int64` | - | `-1` | Max message count (-1 = unlimited) |
| `max_bytes` | `*int64` | - | `-1` | Max storage bytes (-1 = unlimited) |
| `max_msgs_per_subject` | `int64` | - | `0` | Max messages per subject (0 = unlimited) |
| `max_consumers` | `int` | - | `0` | Max consumers allowed (0 or -1 = unlimited) |

#### PublisherConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `count_per_stream` | `int32` | ✓ | - | Publishers per stream |
| `stream_name_prefix` | `string` | ✓ | - | Target stream prefix to match |
| `publish_rate_per_second` | `int64` | ✓ | - | Messages/sec per publisher |
| `publish_pattern` | `string` | ✓ | - | Pattern: `steady`, `random` |
| `publish_burst_size` | `int32` | ✓ | - | Messages published per tick (applies to both patterns) |
| `message_size_bytes` | `int32` | ✓ | - | Message payload size |
| `track_latency` | `bool` | ✓ | - | Enable end-to-end latency tracking |

#### ConsumerConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `start` | `bool` | - | `true` | Whether to start the consumer after creation (false = create but don't subscribe to messages) |
| `stream_name_prefix` | `string` | ✓ | - | Source stream prefix to match |
| `type` | `string` | ✓ | - | Consumer type: `push`, `pull` |
| `count_per_stream` | `int32` | ✓ | - | Consumers per stream |
| `durable_name_prefix` | `string` | ✓ | - | Durable consumer name prefix |
| `ack_wait_seconds` | `int64` | ✓ | - | Message acknowledgment timeout |
| `max_ack_pending` | `int32` | ✓ | - | Max unacknowledged messages per consumer (-1 = unlimited) |
| `consume_delay_ms` | `int64` | - | `0` | Artificial processing delay |
| `ack_policy` | `string` | ✓ | - | Ack policy: `explicit`, `none`, `all` |
| `pull_max_messages` | `int32` | - | `100` | Buffer size for pull consumers (messages to fetch per request) |
| `acknowledge_messages` | `bool` | - | `true` | Whether to acknowledge messages after receiving them (false = receive but never ACK) |

#### BehaviorConfig

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `duration_seconds` | `int64` | ✓ | - | Total test duration |
| `ramp_up_seconds` | `int64` | ✓ | - | Gradual rate increase period |

#### LogLimits

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `max_lines` | `int32` | - | `1000` | Maximum error log lines to retain |
| `max_bytes` | `int64` | - | `1048576` | Maximum error log size in bytes |
| `max_latency_samples` | `int32` | - | `10000` | Ring buffer size for latency tracking |

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
  "load_test_specs": [
    {
      "name": "simple_high_throughput",
      "nats_url": "${NATS_URL}",
      "nats_creds_file": "${NATS_CREDS_MOUNT_PATH}/admin.creds",
      "use_jetstream": true,
      "client_id_prefix": "load_tester",
      "streams": [
        {
          "name_prefix": "load_stream",
          "count": 10,
          "replicas": 3,
          "subjects": ["test.subject.{}"],
          "messages_per_stream_per_second": 100000000,
          "retention": "limits",
          "max_age": "5m",
          "storage": "file",
          "discard_new_per_subject": false,
          "discard": "old",
          "max_msgs": 1000000000,
          "max_bytes": 299999999,
          "max_consumers": 10000000,
          "max_msgs_per_subject": 10000000
        }
      ],
      "publishers": {
        "count_per_stream": 50,
        "stream_name_prefix": "load_stream",
        "publish_rate_per_second": 100,
        "publish_pattern": "steady",
        "publish_burst_size": 10,
        "message_size_bytes": 1024,
        "track_latency": true
      },
      "consumers": {
        "start": true,
        "stream_name_prefix": "load_stream",
        "type": "push",
        "count_per_stream": 75,
        "durable_name_prefix": "load_consumer",
        "ack_wait_seconds": 5,
        "max_ack_pending": -1,
        "consume_delay_ms": 0,
        "ack_policy": "explicit",
        "acknowledge_messages": true,
        "pull_max_messages": 100
      },
      "behavior": {
        "duration_seconds": 60,
        "ramp_up_seconds": 30
      },
      "log_limits": {
        "max_lines": 100,
        "max_bytes": 32768,
        "max_latency_samples": 10000
      }
    }
  ],
  "repeat_policy": {
    "enabled": true,
    "streams": {
      "count_multiplier": 1,
      "replicas_multiplier": 1,
      "messages_per_stream_per_second_multiplier": 1.1
    },
    "behavior": {
      "duration_multiplier": 1.1,
      "ramp_up_multiplier": 1
    },
    "consumers": {
      "ack_wait_multiplier": 1.1,
      "max_ack_pending_multiplier": 1,
      "consume_delay_multiplier": 1.1,
      "count_per_stream_multiplier": 1
    },
    "publishers": {
      "count_per_stream_multiplier": 1.5,
      "publish_rate_multiplier": 2,
      "message_size_bytes_multiplier": 1
    }
  },
  "storage": {
    "type": "badger",
    "path": "/var/lib/lt/load_test_stats.db"
  },
  "stats_collection_interval_seconds": 5
}
```

## Performance Profiling

pprof profiling endpoints are available via the `--enable-pprof` flag. Enable in deployment and port-forward to collect profiles:

```bash
# Port-forward to pod
kubectl port-forward deployment/nats-load-tester 9481:9481

# Collect profiles (in another terminal)
./hack/collect-profiles.sh 60 ./profiles 9481

# Analyze
go tool pprof -http=:9090 ./profiles/cpu.prof
```

**Available endpoints:** `/debug/pprof/` (cpu, heap, goroutine, block, mutex)

## TODO

- [x] **CLI Operational Modes**: Add `--mode=publish|consume|both` CLI arguments for specialized pod roles
- [x] **NATS API Migration**: Update from deprecated JetStream API to newer `github.com/nats-io/nats.go/jetstream`
- [x] **Enhanced Latency Metrics**: Implemented P50, P90, P99 percentile tracking with configurable ring buffer size and optimized quickselect algorithm
- [ ] **Synchronize the replicas and distribute the load-generation across each pod**
- [ ] **Allow stopping the load test(s) upon reaching configurable failure threshold(s)**
- [ ] **Unified Service Endpoint**: Create master service that accepts single configuration and forwards to all replicated pods
- [ ] **Enhanced Metrics System**: Implement comprehensive metrics collection including system resources (CPU, memory, goroutines), NATS-specific metrics (connection health, bytes in/out), JetStream performance (storage usage, cluster status), throughput trends, error categorization, and test progress tracking. Add Prometheus export, real-time WebSocket streaming, and comparative analysis capabilities for production-grade observability.
