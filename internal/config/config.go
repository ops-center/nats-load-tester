package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

type Config struct {
	LoadTestSpecs                  []LoadTestSpec `json:"load_test_specs"`
	RepeatPolicy                   RepeatPolicy   `json:"repeat_policy"`
	Storage                        Storage        `json:"storage"`
	StatsCollectionIntervalSeconds int64          `json:"stats_collection_interval_seconds"`
}

type LoadTestSpec struct {
	Name           string          `json:"name"`
	NATSURL        string          `json:"nats_url"`
	NATSCredsFile  string          `json:"nats_creds_file,omitempty"`
	UseJetStream   bool            `json:"use_jetstream"`
	ClientIDPrefix string          `json:"client_id_prefix"`
	Streams        []StreamConfig  `json:"streams"`
	Publishers     PublisherConfig `json:"publishers"`
	Consumers      ConsumerConfig  `json:"consumers"`
	Behavior       BehaviorConfig  `json:"behavior"`
	LogLimits      LogLimits       `json:"log_limits"`
}

type StreamConfig struct {
	NamePrefix                 string   `json:"name_prefix"`
	Count                      int      `json:"count"`
	Replicas                   int      `json:"replicas"`
	Subjects                   []string `json:"subjects"`
	MessagesPerStreamPerSecond int64    `json:"messages_per_stream_per_second"`
	MessageSizeBytes           int64    `json:"message_size_bytes"`
	PublishPattern             string   `json:"publish_pattern"`
}

type PublisherConfig struct {
	CountPerStream       int    `json:"count_per_stream"`
	StreamNamePrefix     string `json:"stream_name_prefix"`
	PublishRatePerSecond int    `json:"publish_rate_per_second"`
	MessageSizeBytes     int    `json:"message_size_bytes"`
	TrackLatency         bool   `json:"track_latency"`
}

type ConsumerConfig struct {
	StreamNamePrefix  string `json:"stream_name_prefix"`
	Type              string `json:"type"`
	CountPerStream    int    `json:"count_per_stream"`
	DurableNamePrefix string `json:"durable_name_prefix"`
	AckWaitSeconds    int64  `json:"ack_wait_seconds"`
	MaxAckPending     int    `json:"max_ack_pending"`
	ConsumeDelayMs    int64  `json:"consume_delay_ms"`
	AckPolicy         string `json:"ack_policy"`
}

type BehaviorConfig struct {
	DurationSeconds int64 `json:"duration_seconds"`
	RampUpSeconds   int64 `json:"ramp_up_seconds"`
}

type LogLimits struct {
	MaxLines int   `json:"max_lines"`
	MaxBytes int64 `json:"max_bytes"`
}

type RepeatPolicy struct {
	Enabled    bool                 `json:"enabled"`
	Streams    StreamMultipliers    `json:"streams"`
	Behavior   BehaviorMultipliers  `json:"behavior"`
	Consumers  ConsumerMultipliers  `json:"consumers"`
	Publishers PublisherMultipliers `json:"publishers"`
}

type StreamMultipliers struct {
	CountMultiplier                      float64 `json:"count_multiplier"`
	ReplicasMultiplier                   float64 `json:"replicas_multiplier"`
	MessagesPerStreamPerSecondMultiplier float64 `json:"messages_per_stream_per_second_multiplier"`
}

type BehaviorMultipliers struct {
	DurationMultiplier float64 `json:"duration_multiplier"`
	RampUpMultiplier   float64 `json:"ramp_up_multiplier"`
}

type ConsumerMultipliers struct {
	AckWaitMultiplier       float64 `json:"ack_wait_multiplier"`
	MaxAckPendingMultiplier float64 `json:"max_ack_pending_multiplier"`
	ConsumeDelayMultiplier  float64 `json:"consume_delay_multiplier"`
}

type PublisherMultipliers struct {
	CountPerStreamMultiplier   float64 `json:"count_per_stream_multiplier"`
	PublishRateMultiplier      float64 `json:"publish_rate_multiplier"`
	MessageSizeBytesMultiplier float64 `json:"message_size_bytes_multiplier"`
}

type Storage struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

func (c *Config) Validate() error {
	if len(c.LoadTestSpecs) == 0 {
		return fmt.Errorf("at least one configuration required")
	}

	for i, cfg := range c.LoadTestSpecs {
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("configuration %d: %w", i, err)
		}
	}

	if c.Storage.Type == "" {
		c.Storage.Type = "badger"
	}

	if c.Storage.Path == "" {
		c.Storage.Path = "./load_test_stats.db"
	}

	if c.StatsCollectionIntervalSeconds <= 0 {
		c.StatsCollectionIntervalSeconds = 5
	}

	return nil
}

func (c *LoadTestSpec) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name required")
	}

	if c.NATSURL == "" {
		return fmt.Errorf("nats_url required")
	}

	if c.ClientIDPrefix == "" {
		c.ClientIDPrefix = "load-tester"
	}

	if len(c.Streams) == 0 {
		return fmt.Errorf("at least one stream configuration required")
	}

	for i, stream := range c.Streams {
		if err := stream.Validate(); err != nil {
			return fmt.Errorf("stream %d: %w", i, err)
		}
	}

	if err := c.Publishers.Validate(); err != nil {
		return fmt.Errorf("publishers: %w", err)
	}

	if err := c.Consumers.Validate(); err != nil {
		return fmt.Errorf("consumers: %w", err)
	}

	if err := c.Behavior.Validate(); err != nil {
		return fmt.Errorf("behavior: %w", err)
	}

	return nil
}

func (s *StreamConfig) Validate() error {
	if s.NamePrefix == "" {
		return fmt.Errorf("name_prefix required")
	}

	if s.Count <= 0 {
		return fmt.Errorf("count must be positive")
	}

	if s.Replicas <= 0 {
		s.Replicas = 1
	}

	if len(s.Subjects) == 0 {
		return fmt.Errorf("at least one subject required")
	}

	if s.MessagesPerStreamPerSecond <= 0 {
		s.MessagesPerStreamPerSecond = 100
	}

	if s.MessageSizeBytes <= 0 {
		s.MessageSizeBytes = 256
	}

	if s.PublishPattern == "" {
		s.PublishPattern = "steady"
	}

	return nil
}

func (p *PublisherConfig) Validate() error {
	if p.CountPerStream <= 0 {
		p.CountPerStream = 1
	}

	if p.StreamNamePrefix == "" {
		return fmt.Errorf("stream_name_prefix required")
	}

	if p.PublishRatePerSecond <= 0 {
		p.PublishRatePerSecond = 100
	}

	if p.MessageSizeBytes <= 0 {
		p.MessageSizeBytes = 1024
	}

	return nil
}

func (c *ConsumerConfig) Validate() error {
	if c.StreamNamePrefix == "" {
		return fmt.Errorf("stream_name_prefix required")
	}

	if c.Type != "push" && c.Type != "pull" {
		c.Type = "push"
	}

	if c.CountPerStream <= 0 {
		c.CountPerStream = 1
	}

	if c.DurableNamePrefix == "" {
		c.DurableNamePrefix = "load_consumer"
	}

	if c.AckWaitSeconds <= 0 {
		c.AckWaitSeconds = 30
	}

	if c.MaxAckPending <= 0 {
		c.MaxAckPending = 1000
	}

	if c.AckPolicy == "" {
		c.AckPolicy = "explicit"
	}

	return nil
}

func (b *BehaviorConfig) Validate() error {
	if b.DurationSeconds <= 0 {
		b.DurationSeconds = 600
	}

	if b.RampUpSeconds <= 0 {
		b.RampUpSeconds = 1
	}

	return nil
}

func (c *Config) Hash() string {
	data, _ := json.Marshal(c)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func (c *LoadTestSpec) Hash() string {
	data, _ := json.Marshal(c)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func (c *LoadTestSpec) ApplyMultipliers(rp RepeatPolicy) {
	for i := range c.Streams {
		c.Streams[i].Count = int(float64(c.Streams[i].Count) * rp.Streams.CountMultiplier)
		c.Streams[i].Replicas = int(float64(c.Streams[i].Replicas) * rp.Streams.ReplicasMultiplier)
		c.Streams[i].MessagesPerStreamPerSecond = int64(float64(c.Streams[i].MessagesPerStreamPerSecond) * rp.Streams.MessagesPerStreamPerSecondMultiplier)
	}

	c.Behavior.DurationSeconds = int64(float64(c.Behavior.DurationSeconds) * rp.Behavior.DurationMultiplier)
	c.Behavior.RampUpSeconds = int64(float64(c.Behavior.RampUpSeconds) * rp.Behavior.RampUpMultiplier)

	c.Consumers.AckWaitSeconds = int64(float64(c.Consumers.AckWaitSeconds) * rp.Consumers.AckWaitMultiplier)
	c.Consumers.MaxAckPending = int(float64(c.Consumers.MaxAckPending) * rp.Consumers.MaxAckPendingMultiplier)
	c.Consumers.ConsumeDelayMs = int64(float64(c.Consumers.ConsumeDelayMs) * rp.Consumers.ConsumeDelayMultiplier)

	c.Publishers.CountPerStream = int(float64(c.Publishers.CountPerStream) * rp.Publishers.CountPerStreamMultiplier)
	c.Publishers.PublishRatePerSecond = int(float64(c.Publishers.PublishRatePerSecond) * rp.Publishers.PublishRateMultiplier)
	c.Publishers.MessageSizeBytes = int(float64(c.Publishers.MessageSizeBytes) * rp.Publishers.MessageSizeBytesMultiplier)
}

func (c *Config) StatsInterval() time.Duration {
	return time.Duration(c.StatsCollectionIntervalSeconds) * time.Second
}

func (c *LoadTestSpec) Duration() time.Duration {
	return time.Duration(c.Behavior.DurationSeconds) * time.Second
}

func (c *LoadTestSpec) RampUpDuration() time.Duration {
	return time.Duration(c.Behavior.RampUpSeconds) * time.Second
}
