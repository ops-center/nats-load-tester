# 🚧 TODO 🚧
- ~~fix the stream/publisher/consumer subjects synchronization and/or validation~~.
- ~~implement 'GetFailures()' method for reading runtime failures.~~
- ~~improve performance for file storage.~~
- make streams more configurable (e.g. storage type, max msgs/bytes, retention policy, etc.).
- support load testing nats clusters outside of k8s clusters, configured through args/envs.
------

## NATS Load Tester

A dynamically configurable load testing tool for NATS messaging systems with real-time configuration updates via HTTP API.

## API Endpoints

### Configuration Management

#### `POST /config`
Submit a new load test configuration. Cancels any currently running test and starts the new one. See [Configuration Schema](#configuration-schema) for complete payload structure.

**Request:** `application/json`

**Example Request:**
```bash
curl -X POST http://localhost:9481/config \
  -H "Content-Type: application/json" \
  -d '{
    "load_test_specs": [
      {
        "name": "example-load-test",
        "nats_url": "nats://localhost:4222",
        "use_jetstream": true,
        "client_id_prefix": "load-tester",
        "streams": [
          {
            "name_prefix": "test_stream",
            "count": 5,  
            "replicas": 1,
            "subjects": ["orders.{}", "payments.{}"],
            "messages_per_stream_per_second": 100,
            "message_size_bytes": 512
          }
        ],
        "publishers": {
          "count_per_stream": 1,
          "stream_name_prefix": "test_stream",
          "publish_rate_per_second": 100,
          "publish_pattern": "steady",
          "publish_burst_size": 1,
          "message_size_bytes": 512,
          "track_latency": true
        },
        "consumers": {
          "stream_name_prefix": "test_stream",
          "type": "push",
          "count_per_stream": 1,
          "durable_name_prefix": "test_consumer",
          "ack_wait_seconds": 30,
          "max_ack_pending": 1000,
          "consume_delay_ms": 0,
          "ack_policy": "explicit"
        },
        "behavior": {
          "duration_seconds": 300,
          "ramp_up_seconds": 30
        },
        "log_limits": {
          "max_lines": 200,
          "max_bytes": 65536
        }
      }
    ],
    "repeat_policy": {
      "enabled": false
    },
    "storage": {
      "type": "badger",
      "path": "./test_stats.db"
    },
    "stats_collection_interval_seconds": 5
  }'
```

**Response:** `200 OK`
```json
{
  "queued": true,
  "hash": "a1b2c3d4e5f6..."
}
```

**Error Responses:**
- `400 Bad Request` - Invalid JSON or configuration validation error
- `503 Service Unavailable` - Server busy processing another configuration (5s timeout)

#### `GET /config`
Retrieve the currently active configuration.

**Example Request:**
```bash
curl http://localhost:9481/config
```

**Response:** `200 OK` with current config JSON
```json
{
  "load_test_specs": [
    {
      "name": "example-load-test",
      "nats_url": "nats://localhost:4222",
      // ... complete configuration
    }
  ],
  "repeat_policy": { "enabled": false },
  "storage": { "type": "badger", "path": "./test_stats.db" },
  "stats_collection_interval_seconds": 5
}
```

**Error Responses:**
- `404 Not Found` - No configuration currently set

### Statistics

#### `GET /stats/history`
Retrieve statistics history from the active collector.

**Example Request:**
```bash
curl http://localhost:9481/stats/history
```

**Response:** `200 OK`
```json
[
  {
    "timestamp": "2023-12-01T10:00:00Z",
    "config_hash": "a1b2c3d4e5f6...",
    "published": 5000,
    "consumed": 4950,
    "publish_rate": 100.5,
    "consume_rate": 99.8,
    "publish_errors": 2,
    "consume_errors": 1,
    "pending_messages": 50,
    "latency": {
      "count": 4950,
      "min": 1500000,
      "max": 15000000,
      "mean": 3500000,
      "p99": 12000000
    },
    "errors": []
  }
]
```

**Error Responses:**
- `503 Service Unavailable` - No active collector available

### Health Check

#### `GET /health`
Basic health check endpoint.

**Example Request:**
```bash
curl http://localhost:9481/health
```

**Response:** `200 OK`
```json
{
  "status": "healthy",
  "time": "2023-12-01T10:00:00Z"
}
```

## Configuration Schema

> **⚠️ Strict Validation**: All configuration fields marked as "Required" must be provided with valid values. The system uses strict validation and will not apply defaults for missing fields. All numeric fields must be positive values where indicated.

### Root Configuration
```json
{
  "load_test_specs": [
    {
      "name": "example-test",
      "nats_url": "nats://localhost:4222",
      "use_jetstream": true,
      "streams": [...],
      "publishers": {...},
      "consumers": {...},
      "behavior": {...}
    }
  ],
  "repeat_policy": {
    "enabled": false
  },
  "storage": {
    "type": "badger",
    "path": "./load_test_stats.db"
  },
  "stats_collection_interval_seconds": 5
}
```

| Field | Type | Description | Default |
|-------|------|-------------|---------|
| `load_test_specs` | `[]LoadTestSpec` | Array of load test configurations to execute sequentially | Required |
| `repeat_policy` | `RepeatPolicy` | Configuration for repeating the last test spec with multipliers | Optional |
| `storage` | `Storage` | Statistics storage configuration | `{"type": "badger", "path": "./load_test_stats.db"}` |
| `stats_collection_interval_seconds` | `int64` | Interval for collecting statistics snapshots | `5` |

### LoadTestSpec
Individual load test configuration defining NATS connection, streams, publishers, consumers, and behavior.

```json
{
  "name": "my-load-test",
  "nats_url": "nats://localhost:4222",
  "nats_creds_file": "/path/to/creds.json",
  "use_jetstream": true,
  "client_id_prefix": "load-tester",
  "streams": [StreamConfig],
  "publishers": PublisherConfig,
  "consumers": ConsumerConfig,
  "behavior": BehaviorConfig,
  "log_limits": LogLimits
}
```

| Field | Type | Description | Default |
|-------|------|-------------|---------|
| `name` | `string` | Unique identifier for this test configuration | Required |
| `nats_url` | `string` | NATS server URL | Required |
| `nats_creds_file` | `string` | Path to NATS credentials file | Optional |
| `use_jetstream` | `bool` | Enable JetStream for persistent messaging | `false` |
| `client_id_prefix` | `string` | Prefix for NATS client IDs | `"load-tester"` |
| `streams` | `[]StreamConfig` | JetStream stream configurations | Required |
| `publishers` | `PublisherConfig` | Publisher configuration | Required |
| `consumers` | `ConsumerConfig` | Consumer configuration | Required |
| `behavior` | `BehaviorConfig` | Test execution behavior settings | Required |
| `log_limits` | `LogLimits` | Logging constraints | Optional |

### StreamConfig
JetStream stream configuration defining subjects, replication, and message throughput.

```json
{
  "name_prefix": "test-stream",
  "count": 3,
  "replicas": 3,
  "subjects": ["orders.{}", "payments.{}"],
  "messages_per_stream_per_second": 1000,
  "message_size_bytes": 1024
}
```

| Field | Type | Description | Required |
|-------|------|-------------|----------|
| `name_prefix` | `string` | Prefix for generated stream names | Yes |
| `count` | `int` | Number of streams to create | Yes |
| `replicas` | `int` | JetStream replica count per stream | Yes |
| `subjects` | `[]string` | NATS subjects for the streams. Use `{}` placeholders for indexed subjects (automatically converted to `%d` format), or static subjects without placeholders. | Yes |
| `messages_per_stream_per_second` | `int64` | Target message rate per stream | Yes |
| `message_size_bytes` | `int64` | Size of each message in bytes | Yes |

### PublisherConfig
Configuration for message publishers that write to JetStream.

```json
{
  "count_per_stream": 2,
  "stream_name_prefix": "test-stream",
  "publish_rate_per_second": 500,
  "publish_pattern": "steady",
  "publish_burst_size": 1,
  "message_size_bytes": 1024,
  "track_latency": true
}
```

| Field | Type | Description | Required |
|-------|------|-------------|----------|
| `count_per_stream` | `int` | Number of publishers per stream | Yes |
| `stream_name_prefix` | `string` | Prefix to match target streams | Yes |
| `publish_rate_per_second` | `int` | Messages per second per publisher | Yes |
| `publish_pattern` | `string` | Message publishing pattern: "steady" or "random" | Yes |
| `publish_burst_size` | `int` | Number of messages to send in burst when using random pattern | Yes |
| `message_size_bytes` | `int` | Size of published messages | Yes |
| `track_latency` | `bool` | Enable end-to-end latency measurement | Yes |

### ConsumerConfig
Configuration for message consumers that read from JetStream.

```json
{
  "stream_name_prefix": "test-stream",
  "type": "push",
  "count_per_stream": 1,
  "durable_name_prefix": "load_consumer",
  "ack_wait_seconds": 30,
  "max_ack_pending": 1000,
  "consume_delay_ms": 0,
  "ack_policy": "explicit"
}
```

| Field | Type | Description | Required |
|-------|------|-------------|----------|
| `stream_name_prefix` | `string` | Prefix to match source streams | Yes |
| `type` | `string` | Consumer type: "push" or "pull" | Yes |
| `count_per_stream` | `int` | Number of consumers per stream | Yes |
| `durable_name_prefix` | `string` | Prefix for durable consumer names | Yes |
| `ack_wait_seconds` | `int64` | Acknowledgment timeout in seconds | Yes |
| `max_ack_pending` | `int` | Maximum unacknowledged messages | Yes |
| `consume_delay_ms` | `int64` | Artificial delay between message processing | No |
| `ack_policy` | `string` | JetStream ack policy: "explicit", "none", "all" | Yes |

### BehaviorConfig
Test execution timing and behavior configuration.

```json
{
  "duration_seconds": 300,
  "ramp_up_seconds": 30
}
```

| Field | Type | Description | Required |
|-------|------|-------------|----------|
| `duration_seconds` | `int64` | Total test duration in seconds | Yes |
| `ramp_up_seconds` | `int64` | Gradual ramp-up period for publishers/consumers | Yes |

### Storage
Statistics storage backend configuration with multiple options for persistence and output.

```json
{
  "type": "badger",
  "path": "/data/stats.db"
}
```

| Field | Type | Description | Default |
|-------|------|-------------|---------|
| `type` | `string` | Storage backend type | `"badger"` |
| `path` | `string` | Storage location | `"./load_test_stats.db"` |

#### Storage Types

**`"file"`** - Human-readable file output
- **Path**: File path for statistics output
- **Format**: Structured text with timestamps, metrics, and latency data
- **Use Case**: Development, debugging, simple deployments
- **Example**: `{"type": "file", "path": "./load_test_stats.log"}`

**`"badger"`** - BadgerDB embedded database (default)
- **Path**: Directory path for BadgerDB storage
- **Format**: Structured JSON with automatic TTL and garbage collection
- **Features**:
  - Stats data TTL: 24 hours
  - Config data TTL: 7 days
  - Failure data TTL: 72 hours
  - Automatic value log garbage collection
  - Singleton pattern prevents multiple database opens
- **Use Case**: Production deployments, persistent storage, high-throughput scenarios
- **Example**: `{"type": "badger", "path": "/data/stats.db"}`

**`"stdout"`** - Console output (special file case)
- **Path**: Uses `/dev/stdout` internally
- **Format**: Same as file but outputs to console
- **Use Case**: Containerized environments, CI/CD pipelines, debugging
- **Example**: `{"type": "stdout", "path": ""}`

### RepeatPolicy
Configuration for repeating tests with scaled parameters.

```json
{
  "enabled": true,
  "streams": {
    "count_multiplier": 1.5,
    "replicas_multiplier": 1.0,
    "messages_per_stream_per_second_multiplier": 2.0
  },
  "behavior": {
    "duration_multiplier": 1.0,
    "ramp_up_multiplier": 1.0
  },
  "consumers": {
    "ack_wait_multiplier": 1.0,
    "max_ack_pending_multiplier": 1.0,
    "consume_delay_multiplier": 1.0
  },
  "publishers": {
    "count_per_stream_multiplier": 1.0,
    "publish_rate_multiplier": 1.5,
    "message_size_bytes_multiplier": 1.0
  }
}
```

All multiplier fields are `float64` values that scale the corresponding configuration parameters when repeating the last test specification.
