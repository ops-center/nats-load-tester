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

package config

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Validation constants
const (
	ConsumerTypePush     = "push"
	ConsumerTypePull     = "pull"
	defaultStatsInterval = 5
)

const (
	BadgerDBStorage = "badger"
	FileStorage     = "file"
	StdoutStorage   = "stdout"

	defaultBadgerDBPath      = "./load_test_stats.db"
	defaultFileStoragePath   = "./load_test_stats.log"
	defaultStdoutStoragePath = "/dev/stdout"
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
	NATSCredsFile  string          `json:"nats_creds_file"`
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
	Retention            string `json:"retention"`                         // "limits", "interest", "workqueue" - defaults to "limits"
	MaxAge               string `json:"max_age"`                           // duration string like "1h", "30m" - defaults to "1m"
	Storage              string `json:"storage"`                           // "file", "memory" - defaults to "memory"
	DiscardNewPerSubject *bool  `json:"discard_new_per_subject,omitempty"` // defaults to false
	Discard              string `json:"discard"`                           // "old", "new" - defaults to "old"
	MaxMsgs              *int64 `json:"max_msgs,omitempty"`                // maximum number of messages - defaults to -1 (unlimited)
	MaxBytes             *int64 `json:"max_bytes,omitempty"`               // maximum total size of messages - defaults to -1 (unlimited)
	MaxMsgsPerSubject    *int64 `json:"max_msgs_per_subject,omitempty"`    // maximum messages per subject - defaults to -1 (unlimited)
	MaxConsumers         *int   `json:"max_consumers,omitempty"`           // maximum number of consumers - defaults to -1 (unlimited)
}

type PublisherConfig struct {
	CountPerStream       int32  `json:"count_per_stream"`
	StreamNamePrefix     string `json:"stream_name_prefix"`
	PublishRatePerSecond int64  `json:"publish_rate_per_second"`
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
	MaxLines          int32 `json:"max_lines"`
	MaxBytes          int64 `json:"max_bytes"`
	MaxLatencySamples int32 `json:"max_latency_samples"`
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
	AckWaitMultiplier        float64 `json:"ack_wait_multiplier"`
	MaxAckPendingMultiplier  float64 `json:"max_ack_pending_multiplier"`
	ConsumeDelayMultiplier   float64 `json:"consume_delay_multiplier"`
	CountPerStreamMultiplier float64 `json:"count_per_stream_multiplier"`
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

	for i, loadTestSpec := range c.LoadTestSpecs {
		if err := loadTestSpec.Validate(); err != nil {
			return fmt.Errorf("configuration %d: %w", i, err)
		}
	}

	if err := c.RepeatPolicy.Validate(); err != nil {
		return fmt.Errorf("repeat_policy: %w", err)
	}

	if c.Storage.Type == "" {
		c.Storage.Type = BadgerDBStorage
	}

	if c.Storage.Type != BadgerDBStorage && c.Storage.Type != FileStorage && c.Storage.Type != StdoutStorage {
		return fmt.Errorf("unknown storage type '%s'", c.Storage.Type)
	}

	if c.Storage.Path == "" {
		switch c.Storage.Type {
		case FileStorage:
			c.Storage.Path = defaultFileStoragePath
		case StdoutStorage:
			c.Storage.Path = defaultStdoutStoragePath
		case BadgerDBStorage:
			c.Storage.Path = defaultBadgerDBPath
		default:
			return fmt.Errorf("storage path required for storage type '%s'", c.Storage.Type)
		}
	}

	if c.StatsCollectionIntervalSeconds <= 0 {
		c.StatsCollectionIntervalSeconds = defaultStatsInterval
	}

	return nil
}

func (lts *LoadTestSpec) Validate() error {
	if lts == nil {
		return fmt.Errorf("load test spec is nil")
	}
	loadTestValidationErrors := []error{}
	if lts.Name == "" {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("name required"))
	}

	if lts.NATSURL == "" {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("nats_url required"))
	}

	if len(lts.Streams) == 0 {
		loadTestValidationErrors = append(loadTestValidationErrors, fmt.Errorf("at least one stream required"))
	}

	for i := range lts.Streams {
		if err := lts.Streams[i].Validate(); err != nil {
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

	if lts.ClientIDPrefix == "" {
		lts.ClientIDPrefix = "load-tester"
	}

	return errors.Join(loadTestValidationErrors...)
}

func (s *StreamSpec) Validate() error {
	if s == nil {
		return fmt.Errorf("stream spec is nil")
	}
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

	if s.Discard == DiscardOld && s.DiscardNewPerSubject != nil && *s.DiscardNewPerSubject {
		streamValidationErrors = append(streamValidationErrors, fmt.Errorf("discard_new_per_subject can only be 'true' when discard is '%s'", DiscardNew))
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
		s.Subjects[i] = strings.ReplaceAll(s.Subjects[i], "{}", "%d")
	}

	return nil
}

func (p *PublisherConfig) Validate() error {
	if p == nil {
		return fmt.Errorf("publisher config is nil")
	}
	publishValidationErrors := []error{}

	if p.StreamNamePrefix == "" {
		publishValidationErrors = append(publishValidationErrors, fmt.Errorf("stream_name_prefix required"))
	}

	if p.CountPerStream <= 0 {
		publishValidationErrors = append(publishValidationErrors, fmt.Errorf("count_per_stream must be positive, got %d", p.CountPerStream))
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

	if c.CountPerStream < 0 {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("count_per_stream must be non-negative, got %d", c.CountPerStream))
	}

	if c.Type != ConsumerTypePush && c.Type != ConsumerTypePull {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("type must be '%s' or '%s', got '%s'", ConsumerTypePush, ConsumerTypePull, c.Type))
	}

	if c.DurableNamePrefix == "" {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("durable_name_prefix required"))
	}

	if c.AckWaitSeconds < 0 {
		consumerValidationErrors = append(consumerValidationErrors, fmt.Errorf("ack_wait_seconds must be non-negative, got %d", c.AckWaitSeconds))
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

	if b.RampUpSeconds < 0 {
		behaviorValidationErrors = append(behaviorValidationErrors, fmt.Errorf("ramp_up_seconds must be non-negative, got %d", b.RampUpSeconds))
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

func (rp *RepeatPolicy) Validate() error {
	if !rp.Enabled {
		return nil
	}

	var repeatPolicyErrors []error
	if err := rp.Streams.Validate(); err != nil {
		repeatPolicyErrors = append(repeatPolicyErrors, fmt.Errorf("streams: %w", err))
	}

	if err := rp.Behavior.Validate(); err != nil {
		repeatPolicyErrors = append(repeatPolicyErrors, fmt.Errorf("behavior: %w", err))
	}

	if err := rp.Consumers.Validate(); err != nil {
		repeatPolicyErrors = append(repeatPolicyErrors, fmt.Errorf("consumers: %w", err))
	}

	if err := rp.Publishers.Validate(); err != nil {
		repeatPolicyErrors = append(repeatPolicyErrors, fmt.Errorf("publishers: %w", err))
	}

	return errors.Join(repeatPolicyErrors...)
}

func (s *StreamMultipliers) Validate() error {
	if s == nil {
		return fmt.Errorf("stream multipliers required when repeat_policy is enabled")
	}
	var streamMultiplierErrors []error
	if s.CountMultiplier < 1.0 {
		streamMultiplierErrors = append(streamMultiplierErrors, fmt.Errorf("count_multiplier must be >= 1.0, got %f", s.CountMultiplier))
	}

	if s.ReplicasMultiplier < 1.0 {
		streamMultiplierErrors = append(streamMultiplierErrors, fmt.Errorf("replicas_multiplier must be >= 1.0, got %f", s.ReplicasMultiplier))
	}

	if s.MessagesPerStreamPerSecondMultiplier < 1.0 {
		streamMultiplierErrors = append(streamMultiplierErrors, fmt.Errorf("messages_per_stream_per_second_multiplier must be >= 1.0, got %f", s.MessagesPerStreamPerSecondMultiplier))
	}

	return errors.Join(streamMultiplierErrors...)
}

func (b *BehaviorMultipliers) Validate() error {
	if b == nil {
		return fmt.Errorf("behavior multipliers required when repeat_policy is enabled")
	}
	var behaviorMultiplierErrors []error
	if b.DurationMultiplier < 1.0 {
		behaviorMultiplierErrors = append(behaviorMultiplierErrors, fmt.Errorf("duration_multiplier must be >= 1.0, got %f", b.DurationMultiplier))
	}
	if b.RampUpMultiplier < 1.0 {
		behaviorMultiplierErrors = append(behaviorMultiplierErrors, fmt.Errorf("ramp_up_multiplier must be >= 1.0, got %f", b.RampUpMultiplier))
	}

	return errors.Join(behaviorMultiplierErrors...)
}

func (c *ConsumerMultipliers) Validate() error {
	if c == nil {
		return fmt.Errorf("consumer multipliers required when repeat_policy is enabled")
	}
	var consumerMultiplierErrors []error
	if c.AckWaitMultiplier < 1.0 {
		consumerMultiplierErrors = append(consumerMultiplierErrors, fmt.Errorf("ack_wait_multiplier must be >= 1.0, got %f", c.AckWaitMultiplier))
	}

	if c.MaxAckPendingMultiplier < 1.0 {
		consumerMultiplierErrors = append(consumerMultiplierErrors, fmt.Errorf("max_ack_pending_multiplier must be >= 1.0, got %f", c.MaxAckPendingMultiplier))
	}

	if c.ConsumeDelayMultiplier < 1.0 {
		consumerMultiplierErrors = append(consumerMultiplierErrors, fmt.Errorf("consume_delay_multiplier must be >= 1.0, got %f", c.ConsumeDelayMultiplier))
	}

	if c.CountPerStreamMultiplier < 1.0 {
		consumerMultiplierErrors = append(consumerMultiplierErrors, fmt.Errorf("count_per_stream_multiplier must be >= 1.0, got %f", c.CountPerStreamMultiplier))
	}

	return errors.Join(consumerMultiplierErrors...)
}

func (p *PublisherMultipliers) Validate() error {
	if p == nil {
		return fmt.Errorf("publisher multipliers required when repeat_policy is enabled")
	}
	var publisherMultiplierErrors []error
	if p.CountPerStreamMultiplier < 1.0 {
		publisherMultiplierErrors = append(publisherMultiplierErrors, fmt.Errorf("count_per_stream_multiplier must be >= 1.0, got %f", p.CountPerStreamMultiplier))
	}

	if p.PublishRateMultiplier < 1.0 {
		publisherMultiplierErrors = append(publisherMultiplierErrors, fmt.Errorf("publish_rate_multiplier must be >= 1.0, got %f", p.PublishRateMultiplier))
	}

	if p.MessageSizeBytesMultiplier < 1.0 {
		publisherMultiplierErrors = append(publisherMultiplierErrors, fmt.Errorf("message_size_bytes_multiplier must be >= 1.0, got %f", p.MessageSizeBytesMultiplier))
	}
	return errors.Join(publisherMultiplierErrors...)
}

func (lts *LoadTestSpec) ApplyMultipliers(rp RepeatPolicy) {
	original := *lts

	lts.Streams = make([]StreamSpec, len(original.Streams))
	for i := range original.Streams {
		lts.Streams[i] = original.Streams[i]
		lts.Streams[i].Count = int32(float64(original.Streams[i].Count) * rp.Streams.CountMultiplier)
		lts.Streams[i].Replicas = int32(float64(original.Streams[i].Replicas) * rp.Streams.ReplicasMultiplier)
		lts.Streams[i].MessagesPerStreamPerSecond = int64(float64(original.Streams[i].MessagesPerStreamPerSecond) * rp.Streams.MessagesPerStreamPerSecondMultiplier)
	}

	lts.Behavior.DurationSeconds = int64(float64(original.Behavior.DurationSeconds) * rp.Behavior.DurationMultiplier)
	lts.Behavior.RampUpSeconds = int64(float64(original.Behavior.RampUpSeconds) * rp.Behavior.RampUpMultiplier)

	lts.Consumers.AckWaitSeconds = int64(float64(original.Consumers.AckWaitSeconds) * rp.Consumers.AckWaitMultiplier)
	lts.Consumers.MaxAckPending = int32(float64(original.Consumers.MaxAckPending) * rp.Consumers.MaxAckPendingMultiplier)
	lts.Consumers.ConsumeDelayMs = int64(float64(original.Consumers.ConsumeDelayMs) * rp.Consumers.ConsumeDelayMultiplier)
	lts.Consumers.CountPerStream = int32(float64(original.Consumers.CountPerStream) * rp.Consumers.CountPerStreamMultiplier)

	lts.Publishers.CountPerStream = int32(float64(original.Publishers.CountPerStream) * rp.Publishers.CountPerStreamMultiplier)
	lts.Publishers.PublishRatePerSecond = int64(float64(original.Publishers.PublishRatePerSecond) * rp.Publishers.PublishRateMultiplier)
	lts.Publishers.MessageSizeBytes = int32(float64(original.Publishers.MessageSizeBytes) * rp.Publishers.MessageSizeBytesMultiplier)
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
	// Validate that publisher.StreamNamePrefix matches at least one stream name_prefix (if publishers are configured)
	if lts.Publishers.CountPerStream > 0 {
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
	}

	// Validate that consumer.StreamNamePrefix matches at least one stream name_prefix (if consumers are configured)
	if lts.Consumers.CountPerStream > 0 {
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

// GetFormattedStreamNames returns all stream names for this stream spec
func (s *StreamSpec) GetFormattedStreamNames() []string {
	streamNames := make([]string, s.Count)
	for i := int32(0); i < s.Count; i++ {
		streamNames[i] = fmt.Sprintf("%s_%d", s.NamePrefix, i+1)
	}
	return streamNames
}

// Stream configuration methods that return NATS types

// GetRetentionPolicy returns the appropriate jetstream.RetentionPolicy for this stream
func (s *StreamSpec) GetRetentionPolicy() jetstream.RetentionPolicy {
	switch s.Retention {
	case RetentionInterest:
		return jetstream.InterestPolicy
	case RetentionWorkQueue:
		return jetstream.WorkQueuePolicy
	default: // RetentionLimits or empty
		return jetstream.LimitsPolicy
	}
}

// GetStorageType returns the appropriate jetstream.StorageType for this stream
func (s *StreamSpec) GetStorageType() jetstream.StorageType {
	switch s.Storage {
	case StorageFile:
		return jetstream.FileStorage
	default: // StorageMemory or empty
		return jetstream.MemoryStorage
	}
}

// GetDiscardPolicy returns the appropriate jetstream.DiscardPolicy for this stream
func (s *StreamSpec) GetDiscardPolicy() jetstream.DiscardPolicy {
	switch s.Discard {
	case DiscardNew:
		return jetstream.DiscardNew
	default: // DiscardOld or empty
		return jetstream.DiscardOld
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
		return false
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
