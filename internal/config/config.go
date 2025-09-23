package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Config struct {
	LoadTestSpecs                  []*LoadTestSpec `json:"load_test_specs"`
	RepeatPolicy                   RepeatPolicy    `json:"repeat_policy"`
	Storage                        Storage         `json:"storage"`
	StatsCollectionIntervalSeconds int64           `json:"stats_collection_interval_seconds"`
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

func (lts *LoadTestSpec) Validate() error {
	if lts.Name == "" {
		return fmt.Errorf("name required")
	}

	if lts.NATSURL == "" {
		return fmt.Errorf("nats_url required")
	}

	if lts.ClientIDPrefix == "" {
		lts.ClientIDPrefix = "load-tester"
	}

	if len(lts.Streams) == 0 {
		return fmt.Errorf("at least one stream configuration required")
	}

	for i, stream := range lts.Streams {
		if err := stream.Validate(); err != nil {
			return fmt.Errorf("stream %d: %w", i, err)
		}
	}

	if err := lts.Publishers.Validate(); err != nil {
		return fmt.Errorf("publishers: %w", err)
	}

	if err := lts.Consumers.Validate(); err != nil {
		return fmt.Errorf("consumers: %w", err)
	}

	if err := lts.Behavior.Validate(); err != nil {
		return fmt.Errorf("behavior: %w", err)
	}

	if err := lts.validateStreamSynchronization(); err != nil {
		return fmt.Errorf("stream synchronization: %w", err)
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

	// Normalize subject format placeholders: convert {} to %d
	for i, subject := range s.Subjects {
		if len(subject) >= 2 && subject[len(subject)-2:] == "{}" {
			s.Subjects[i] = subject[:len(subject)-2] + "%d"
		}
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

func (c *Config) Equals(otherCfg *Config) bool {
	if c == nil || otherCfg == nil {
		return c == nil && otherCfg == nil
	}
	return c.Hash() == otherCfg.Hash()
}

func (lts *LoadTestSpec) Hash() string {
	data, _ := json.Marshal(lts)
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func (lts *LoadTestSpec) ApplyMultipliers(rp RepeatPolicy) {
	for i := range lts.Streams {
		lts.Streams[i].Count = int(float64(lts.Streams[i].Count) * rp.Streams.CountMultiplier)
		lts.Streams[i].Replicas = int(float64(lts.Streams[i].Replicas) * rp.Streams.ReplicasMultiplier)
		lts.Streams[i].MessagesPerStreamPerSecond = int64(float64(lts.Streams[i].MessagesPerStreamPerSecond) * rp.Streams.MessagesPerStreamPerSecondMultiplier)
	}

	lts.Behavior.DurationSeconds = int64(float64(lts.Behavior.DurationSeconds) * rp.Behavior.DurationMultiplier)
	lts.Behavior.RampUpSeconds = int64(float64(lts.Behavior.RampUpSeconds) * rp.Behavior.RampUpMultiplier)

	lts.Consumers.AckWaitSeconds = int64(float64(lts.Consumers.AckWaitSeconds) * rp.Consumers.AckWaitMultiplier)
	lts.Consumers.MaxAckPending = int(float64(lts.Consumers.MaxAckPending) * rp.Consumers.MaxAckPendingMultiplier)
	lts.Consumers.ConsumeDelayMs = int64(float64(lts.Consumers.ConsumeDelayMs) * rp.Consumers.ConsumeDelayMultiplier)

	lts.Publishers.CountPerStream = int(float64(lts.Publishers.CountPerStream) * rp.Publishers.CountPerStreamMultiplier)
	lts.Publishers.PublishRatePerSecond = int(float64(lts.Publishers.PublishRatePerSecond) * rp.Publishers.PublishRateMultiplier)
	lts.Publishers.MessageSizeBytes = int(float64(lts.Publishers.MessageSizeBytes) * rp.Publishers.MessageSizeBytesMultiplier)
}

func (c *Config) StatsInterval() time.Duration {
	return time.Duration(c.StatsCollectionIntervalSeconds) * time.Second
}

func (lts *LoadTestSpec) Duration() time.Duration {
	return time.Duration(lts.Behavior.DurationSeconds) * time.Second
}

func (lts *LoadTestSpec) RampUpDuration() time.Duration {
	return time.Duration(lts.Behavior.RampUpSeconds) * time.Second
}

// validateStreamSynchronization ensures that stream configurations, publishers, and consumers are properly synchronized
func (lts *LoadTestSpec) validateStreamSynchronization() error {
	// Validate that publisher stream_name_prefix matches at least one stream name_prefix
	publisherStreamPrefix := lts.Publishers.StreamNamePrefix
	publisherMatched := false

	for _, stream := range lts.Streams {
		if stream.NamePrefix == publisherStreamPrefix {
			publisherMatched = true
			break
		}
	}

	if !publisherMatched {
		return fmt.Errorf("publisher stream_name_prefix '%s' does not match any stream name_prefix", publisherStreamPrefix)
	}

	// Validate that consumer stream_name_prefix matches at least one stream name_prefix
	consumerStreamPrefix := lts.Consumers.StreamNamePrefix
	consumerMatched := false

	for _, stream := range lts.Streams {
		if stream.NamePrefix == consumerStreamPrefix {
			consumerMatched = true
			break
		}
	}

	if !consumerMatched {
		return fmt.Errorf("consumer stream_name_prefix '%s' does not match any stream name_prefix", consumerStreamPrefix)
	}

	// For now, require publishers and consumers to use the same stream prefix
	if publisherStreamPrefix != consumerStreamPrefix {
		return fmt.Errorf("publisher stream_name_prefix '%s' must match consumer stream_name_prefix '%s'", publisherStreamPrefix, consumerStreamPrefix)
	}

	return nil
}

func containsFormatPlaceholder(subject string) bool {
	return strings.Contains(subject, "{}") || strings.Contains(subject, "%d")
}

// FormatSubject formats a subject template with the given stream index (1-based)
// If the subject has no format placeholder, returns the subject as-is
func (s *StreamConfig) FormatSubject(subjectIndex, streamIndex int) string {
	if subjectIndex >= len(s.Subjects) {
		return ""
	}
	subject := s.Subjects[subjectIndex]
	if containsFormatPlaceholder(subject) {
		return fmt.Sprintf(subject, streamIndex)
	}
	return subject
}

// GetFormattedSubjects returns all subjects for a given stream index (1-based)
// If a subject has no format placeholder, returns the subject as-is
func (s *StreamConfig) GetFormattedSubjects(streamIndex int) []string {
	subjects := make([]string, len(s.Subjects))
	for i, subject := range s.Subjects {
		if containsFormatPlaceholder(subject) {
			subjects[i] = fmt.Sprintf(subject, streamIndex)
		} else {
			subjects[i] = subject
		}
	}
	return subjects
}
