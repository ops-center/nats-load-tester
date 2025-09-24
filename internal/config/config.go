package config

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// Validation constants
const (
	consumerTypePush     = "push"
	consumerTypePull     = "pull"
	storageTypeBadger    = "badger"
	defaultStoragePath   = "./load_test_stats.db"
	defaultStatsInterval = 5
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
	Streams        []StreamSpec    `json:"streams"`
	Publishers     PublisherConfig `json:"publishers"`
	Consumers      ConsumerConfig  `json:"consumers"`
	Behavior       BehaviorConfig  `json:"behavior"`
	LogLimits      LogLimits       `json:"log_limits"`
}

// Publisher publish patterns
const (
	PublishPatternSteady string = "steady"
	PublishPatternRandom string = "random"
)

// Stream retention policies
const (
	RetentionLimits    = "limits"
	RetentionInterest  = "interest"
	RetentionWorkQueue = "workqueue"
)

// Stream storage types
const (
	StorageFile   = "file"
	StorageMemory = "memory"
)

// Stream discard policies
const (
	DiscardOld = "old"
	DiscardNew = "new"
)

type StreamSpec struct {
	NamePrefix                 string   `json:"name_prefix"`
	Count                      int32    `json:"count"`
	Replicas                   int32    `json:"replicas"`
	Subjects                   []string `json:"subjects"`
	MessagesPerStreamPerSecond int64    `json:"messages_per_stream_per_second"`

	// Stream configuration options (optional with defaults)
	Retention            string `json:"retention,omitempty"`               // "limits", "interest", "workqueue" - defaults to "limits"
	MaxAge               string `json:"max_age,omitempty"`                 // duration string like "1h", "30m" - defaults to "1m"
	Storage              string `json:"storage,omitempty"`                 // "file", "memory" - defaults to "memory"
	DiscardNewPerSubject *bool  `json:"discard_new_per_subject,omitempty"` // defaults to true
	Discard              string `json:"discard,omitempty"`                 // "old", "new" - defaults to "old"
	MaxMsgs              *int64 `json:"max_msgs,omitempty"`                // maximum number of messages - defaults to -1 (unlimited)
	MaxBytes             *int64 `json:"max_bytes,omitempty"`               // maximum total size of messages - defaults to -1 (unlimited)
	MaxMsgsPerSubject    *int64 `json:"max_msgs_per_subject,omitempty"`    // maximum messages per subject - defaults to -1 (unlimited)
	MaxConsumers         *int   `json:"max_consumers,omitempty"`           // maximum number of consumers - defaults to -1 (unlimited)
}

type PublisherConfig struct {
	CountPerStream       int32  `json:"count_per_stream"`
	StreamNamePrefix     string `json:"stream_name_prefix"`
	PublishRatePerSecond int32  `json:"publish_rate_per_second"`
	PublishPattern       string `json:"publish_pattern"`
	PublishBurstSize     int32  `json:"publish_burst_size"`
	MessageSizeBytes     int32  `json:"message_size_bytes"`
	TrackLatency         bool   `json:"track_latency"`
}

type ConsumerConfig struct {
	StreamNamePrefix  string `json:"stream_name_prefix"`
	Type              string `json:"type"`
	CountPerStream    int32  `json:"count_per_stream"`
	DurableNamePrefix string `json:"durable_name_prefix"`
	AckWaitSeconds    int64  `json:"ack_wait_seconds"`
	MaxAckPending     int32  `json:"max_ack_pending"`
	ConsumeDelayMs    int64  `json:"consume_delay_ms"`
	AckPolicy         string `json:"ack_policy"`
}

type BehaviorConfig struct {
	DurationSeconds int64 `json:"duration_seconds"`
	RampUpSeconds   int64 `json:"ramp_up_seconds"`
}

type LogLimits struct {
	MaxLines int32 `json:"max_lines"`
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
		c.Storage.Type = storageTypeBadger
	}

	if c.Storage.Path == "" {
		c.Storage.Path = defaultStoragePath
	}

	if c.StatsCollectionIntervalSeconds <= 0 {
		c.StatsCollectionIntervalSeconds = defaultStatsInterval
	}

	return nil
}

func (lts *LoadTestSpec) Validate() error {
	loadTestValidationErrors := []error{}
	if lts.Name == "" {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("name required"))
	}

	if lts.NATSURL == "" {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("nats_url required"))
	}

	if lts.ClientIDPrefix == "" {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("client_id_prefix required"))
	}

	if len(lts.Streams) == 0 {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("at least one stream required"))
	}

	for i, stream := range lts.Streams {
		if err := stream.Validate(); err != nil {
			loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("stream %d: %w", i, err))
		}
	}

	if err := lts.Publishers.Validate(); err != nil {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("publishers: %w", err))
	}

	if err := lts.Consumers.Validate(); err != nil {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("consumers: %w", err))
	}

	if err := lts.Behavior.Validate(); err != nil {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("behavior: %w", err))
	}

	if err := lts.validateStreamSynchronization(); err != nil {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("stream synchronization: %w", err))
	}

	return errors.Join(loadTestValidationErrors...)
}

func (s *StreamSpec) Validate() error {
	streamValidationErrors := []error{}
	if s.NamePrefix == "" {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("name_prefix required"))
	}

	if s.Count <= 0 {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("count must be positive, got %d", s.Count))
	}

	if s.Replicas <= 0 {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("replicas must be positive, got %d", s.Replicas))
	}

	if len(s.Subjects) == 0 {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("at least one subject required"))
	}

	if s.MessagesPerStreamPerSecond <= 0 {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("messages_per_stream_per_second must be positive, got %d", s.MessagesPerStreamPerSecond))
	}

	// Validate optional stream configuration fields
	if s.Retention != "" && s.Retention != RetentionLimits && s.Retention != RetentionInterest && s.Retention != RetentionWorkQueue {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("retention must be '%s', '%s', or '%s', got '%s'", RetentionLimits, RetentionInterest, RetentionWorkQueue, s.Retention))
	}

	if s.Storage != "" && s.Storage != StorageFile && s.Storage != StorageMemory {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("storage must be '%s' or '%s', got '%s'", StorageFile, StorageMemory, s.Storage))
	}

	if s.Discard != "" && s.Discard != DiscardOld && s.Discard != DiscardNew {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("discard must be '%s' or '%s', got '%s'", DiscardOld, DiscardNew, s.Discard))
	}

	if s.MaxAge != "" {
		if _, err := time.ParseDuration(s.MaxAge); err != nil {
			streamValidationErrors = append(streamValidationErrors, fmt.Errorf("max_age must be a valid duration string (e.g., '1h', '30m'), got '%s': %w", s.MaxAge, err))
		}
	}

	// Validate numeric limits (should be non-negative when provided)
	if s.MaxMsgs != nil && *s.MaxMsgs < -1 {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("max_msgs must be -1 (unlimited) or non-negative, got %d", *s.MaxMsgs))
	}

	if s.MaxBytes != nil && *s.MaxBytes < -1 {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("max_bytes must be -1 (unlimited) or non-negative, got %d", *s.MaxBytes))
	}

	if s.MaxMsgsPerSubject != nil && *s.MaxMsgsPerSubject < -1 {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("max_msgs_per_subject must be -1 (unlimited) or non-negative, got %d", *s.MaxMsgsPerSubject))
	}

	if s.MaxConsumers != nil && *s.MaxConsumers < -1 {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("max_consumers must be -1 (unlimited) or non-negative, got %d", *s.MaxConsumers))
	}

	if len(streamValidationErrors) > 0 {
		return errors.Join(streamValidationErrors...)
	}

	for i := range s.Subjects {
		s.Subjects[i] = strings.Replace(s.Subjects[i], "{}", "%d", 1)
	}

	return nil
}

func (p *PublisherConfig) Validate() error {
	publishValidationErrors := []error{}
	if p.CountPerStream <= 0 {
		publishValidationErrors = append(publishValidationErrors, fmt.Errorf("count_per_stream must be positive, got %d", p.CountPerStream))
	}

	if p.StreamNamePrefix == "" {
		publishValidationErrors = append(publishValidationErrors, fmt.Errorf("stream_name_prefix required"))
	}

	if p.PublishRatePerSecond <= 0 {
		publishValidationErrors = append(publishValidationErrors, fmt.Errorf("publish_rate_per_second must be positive, got %d", p.PublishRatePerSecond))
	}

	if p.MessageSizeBytes <= 0 {
		publishValidationErrors = append(publishValidationErrors, fmt.Errorf("message_size_bytes must be positive, got %d", p.MessageSizeBytes))
	}
	if p.PublishBurstSize <= 0 {
		publishValidationErrors = append(publishValidationErrors, fmt.Errorf("publish_burst_size must be positive, got %d", p.PublishBurstSize))
	}

	if p.PublishPattern != PublishPatternSteady && p.PublishPattern != PublishPatternRandom {
		publishValidationErrors = append(publishValidationErrors, fmt.Errorf("unknown publish_pattern '%s'", p.PublishPattern))
	}

	return errors.Join(publishValidationErrors...)
}

func (c *ConsumerConfig) Validate() error {
	consumerValidationErrors := []error{}
	if c.StreamNamePrefix == "" {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("stream_name_prefix required"))
	}

	if c.Type != consumerTypePush && c.Type != consumerTypePull {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("type must be '%s' or '%s', got '%s'", consumerTypePush, consumerTypePull, c.Type))
	}

	if c.CountPerStream <= 0 {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("count_per_stream must be positive, got %d", c.CountPerStream))
	}

	if c.DurableNamePrefix == "" {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("durable_name_prefix required"))
	}

	if c.AckWaitSeconds <= 0 {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("ack_wait_seconds must be positive, got %d", c.AckWaitSeconds))
	}

	if c.MaxAckPending <= 0 {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("max_ack_pending must be positive, got %d", c.MaxAckPending))
	}

	if c.AckPolicy == "" {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("ack_policy required"))
	}

	return errors.Join(consumerValidationErrors...)
}

func (b *BehaviorConfig) Validate() error {
	behaviorValidationErrors := []error{}
	if b.DurationSeconds <= 0 {
		behaviorValidationErrors = append(behaviorValidationErrors, fmt.Errorf("duration_seconds must be positive, got %d", b.DurationSeconds))
	}

	if b.RampUpSeconds <= 0 {
		behaviorValidationErrors = append(behaviorValidationErrors, fmt.Errorf("ramp_up_seconds must be positive, got %d", b.RampUpSeconds))
	}

	return errors.Join(behaviorValidationErrors...)
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
		lts.Streams[i].Count = int32(float64(lts.Streams[i].Count) * rp.Streams.CountMultiplier)
		lts.Streams[i].Replicas = int32(float64(lts.Streams[i].Replicas) * rp.Streams.ReplicasMultiplier)
		lts.Streams[i].MessagesPerStreamPerSecond = int64(float64(lts.Streams[i].MessagesPerStreamPerSecond) * rp.Streams.MessagesPerStreamPerSecondMultiplier)
	}

	lts.Behavior.DurationSeconds = int64(float64(lts.Behavior.DurationSeconds) * rp.Behavior.DurationMultiplier)
	lts.Behavior.RampUpSeconds = int64(float64(lts.Behavior.RampUpSeconds) * rp.Behavior.RampUpMultiplier)

	lts.Consumers.AckWaitSeconds = int64(float64(lts.Consumers.AckWaitSeconds) * rp.Consumers.AckWaitMultiplier)
	lts.Consumers.MaxAckPending = int32(float64(lts.Consumers.MaxAckPending) * rp.Consumers.MaxAckPendingMultiplier)
	lts.Consumers.ConsumeDelayMs = int64(float64(lts.Consumers.ConsumeDelayMs) * rp.Consumers.ConsumeDelayMultiplier)

	lts.Publishers.CountPerStream = int32(float64(lts.Publishers.CountPerStream) * rp.Publishers.CountPerStreamMultiplier)
	lts.Publishers.PublishRatePerSecond = int32(float64(lts.Publishers.PublishRatePerSecond) * rp.Publishers.PublishRateMultiplier)
	lts.Publishers.MessageSizeBytes = int32(float64(lts.Publishers.MessageSizeBytes) * rp.Publishers.MessageSizeBytesMultiplier)
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

// ValidateStreamSynchronization ensures that stream configurations, publishers, and consumers are properly synchronized
func (lts *LoadTestSpec) validateStreamSynchronization() error {
	// For now, require publishers and consumers to use the same stream prefix
	if lts.Publishers.StreamNamePrefix != lts.Consumers.StreamNamePrefix {
		return fmt.Errorf("publisher stream_name_prefix '%s' must match consumer stream_name_prefix '%s'", lts.Publishers.StreamNamePrefix, lts.Consumers.StreamNamePrefix)
	}

	// Validate that publisher.StreamNamePrefix matches at least one stream name_prefix
	publisherMatched := false

	for _, stream := range lts.Streams {
		if stream.NamePrefix == lts.Publishers.StreamNamePrefix {
			publisherMatched = true
			break
		}
	}

	if !publisherMatched {
		return fmt.Errorf("publisher stream_name_prefix '%s' does not match any stream name_prefix", lts.Publishers.StreamNamePrefix)
	}

	// Validate that consumer.StreamNamePrefix matches at least one stream name_prefix
	consumerMatched := false
	for _, stream := range lts.Streams {
		if stream.NamePrefix == lts.Consumers.StreamNamePrefix {
			consumerMatched = true
			break
		}
	}

	if !consumerMatched {
		return fmt.Errorf("consumer stream_name_prefix '%s' does not match any stream name_prefix", lts.Consumers.StreamNamePrefix)
	}

	return nil
}

func containsFormatPlaceholder(subject string) bool {
	return strings.Contains(subject, "%d")
}

// FormatSubject formats a subject template with the given stream index
// If the subject has no format placeholder, returns the subject as-is
func (s *StreamSpec) FormatSubject(subjectIndex, streamIndex int32) string {
	if subjectIndex >= int32(len(s.Subjects)) {
		return ""
	}
	subject := s.Subjects[subjectIndex]
	if containsFormatPlaceholder(subject) {
		return fmt.Sprintf(subject, streamIndex)
	}
	return subject
}

// GetFormattedSubjects returns all subjects for a given stream index
func (s *StreamSpec) GetFormattedSubjects(streamIndex int32) []string {
	subjects := make([]string, len(s.Subjects))
	for i := range s.Subjects {
		subjects[i] = s.FormatSubject(int32(i), streamIndex)
	}
	return subjects
}

// Stream configuration methods that return NATS types

// GetRetentionPolicy returns the appropriate nats.RetentionPolicy for this stream
func (s *StreamSpec) GetRetentionPolicy() nats.RetentionPolicy {
	switch s.Retention {
	case RetentionInterest:
		return nats.InterestPolicy
	case RetentionWorkQueue:
		return nats.WorkQueuePolicy
	default: // RetentionLimits or empty
		return nats.LimitsPolicy
	}
}

// GetStorageType returns the appropriate nats.StorageType for this stream
func (s *StreamSpec) GetStorageType() nats.StorageType {
	switch s.Storage {
	case StorageFile:
		return nats.FileStorage
	default: // StorageMemory or empty
		return nats.MemoryStorage
	}
}

// GetDiscardPolicy returns the appropriate nats.DiscardPolicy for this stream
func (s *StreamSpec) GetDiscardPolicy() nats.DiscardPolicy {
	switch s.Discard {
	case DiscardNew:
		return nats.DiscardNew
	default: // DiscardOld or empty
		return nats.DiscardOld
	}
}

// GetMaxAge returns the parsed max age duration, defaults to 1 minute if empty or invalid
func (s *StreamSpec) GetMaxAge() time.Duration {
	if s.MaxAge == "" {
		return 1 * time.Minute
	}
	duration, err := time.ParseDuration(s.MaxAge)
	if err != nil {
		return 1 * time.Minute // fallback to default
	}
	return duration
}

// GetDiscardNewPerSubject returns the discard_new_per_subject setting, defaults to true if nil
func (s *StreamSpec) GetDiscardNewPerSubject() bool {
	if s.DiscardNewPerSubject == nil {
		return true
	}
	return *s.DiscardNewPerSubject
}

// GetMaxMsgs returns the maximum number of messages, defaults to -1 (unlimited) if nil
func (s *StreamSpec) GetMaxMsgs() int64 {
	if s.MaxMsgs == nil {
		return -1
	}
	return *s.MaxMsgs
}

// GetMaxBytes returns the maximum total size of messages, defaults to -1 (unlimited) if nil
func (s *StreamSpec) GetMaxBytes() int64 {
	if s.MaxBytes == nil {
		return -1
	}
	return *s.MaxBytes
}

// GetMaxMsgsPerSubject returns the maximum messages per subject, defaults to -1 (unlimited) if nil
func (s *StreamSpec) GetMaxMsgsPerSubject() int64 {
	if s.MaxMsgsPerSubject == nil {
		return -1
	}
	return *s.MaxMsgsPerSubject
}

// GetMaxConsumers returns the maximum number of consumers, defaults to -1 (unlimited) if nil
func (s *StreamSpec) GetMaxConsumers() int {
	if s.MaxConsumers == nil {
		return -1
	}
	return *s.MaxConsumers
}
