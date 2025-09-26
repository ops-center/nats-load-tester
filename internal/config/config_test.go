package config

import (
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestStreamSynchronizationValidation(t *testing.T) {
	t.Setenv("POD_NAME", "0")
	tests := []struct {
		name      string
		spec      *LoadTestSpec
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid configuration",
			spec: &LoadTestSpec{
				Name:           "test",
				NATSURL:        "nats://localhost:4222",
				ClientIDPrefix: "test_{pod}",
				Streams: []StreamSpec{
					{
						NamePrefix:                 "test_stream_{pod}",
						Count:                      1,
						Replicas:                   1,
						Subjects:                   []string{"test.subject.{}"},
						MessagesPerStreamPerSecond: 100,
					},
				},
				Publishers: PublisherConfig{
					StreamNamePrefix:     "test_stream_{pod}",
					CountPerStream:       1,
					PublishRatePerSecond: 100,
					PublishPattern:       PublishPatternSteady,
					PublishBurstSize:     1,
					MessageSizeBytes:     256,
					TrackLatency:         false,
				},
				Consumers: ConsumerConfig{
					StreamNamePrefix:  "test_stream_{pod}",
					Type:              "push",
					CountPerStream:    1,
					DurableNamePrefix: "test_consumer_{pod}",
					AckWaitSeconds:    30,
					MaxAckPending:     1000,
					AckPolicy:         "explicit",
				},
				Behavior: BehaviorConfig{
					DurationSeconds: 300,
					RampUpSeconds:   10,
				},
			},
			wantError: false,
		},
		{
			name: "publisher stream prefix mismatch",
			spec: &LoadTestSpec{
				Name:           "test",
				NATSURL:        "nats://localhost:4222",
				ClientIDPrefix: "test_{pod}",
				Streams: []StreamSpec{
					{
						NamePrefix:                 "test_stream_{pod}",
						Count:                      1,
						Replicas:                   1,
						Subjects:                   []string{"test.subject.{}"},
						MessagesPerStreamPerSecond: 100,
					},
				},
				Publishers: PublisherConfig{
					StreamNamePrefix:     "different_stream_{pod}",
					CountPerStream:       1,
					PublishRatePerSecond: 100,
					PublishPattern:       PublishPatternSteady,
					PublishBurstSize:     1,
					MessageSizeBytes:     256,
					TrackLatency:         false,
				},
				Consumers: ConsumerConfig{
					StreamNamePrefix:  "test_stream_{pod}",
					Type:              "push",
					CountPerStream:    1,
					DurableNamePrefix: "test_consumer_{pod}",
					AckWaitSeconds:    30,
					MaxAckPending:     1000,
					AckPolicy:         "explicit",
				},
				Behavior: BehaviorConfig{
					DurationSeconds: 300,
					RampUpSeconds:   10,
				},
			},
			wantError: true,
			errorMsg:  "publisher stream_name_prefix 'different_stream_0' must match consumer stream_name_prefix 'test_stream_0'",
		},
		{
			name: "consumer stream prefix mismatch",
			spec: &LoadTestSpec{
				Name:           "test",
				NATSURL:        "nats://localhost:4222",
				ClientIDPrefix: "test_{pod}",
				Streams: []StreamSpec{
					{
						NamePrefix:                 "test_stream_{pod}",
						Count:                      1,
						Replicas:                   1,
						Subjects:                   []string{"test.subject.{}"},
						MessagesPerStreamPerSecond: 100,
					},
				},
				Publishers: PublisherConfig{
					StreamNamePrefix:     "test_stream_{pod}",
					CountPerStream:       1,
					PublishRatePerSecond: 100,
					PublishPattern:       PublishPatternSteady,
					PublishBurstSize:     1,
					MessageSizeBytes:     256,
					TrackLatency:         false,
				},
				Consumers: ConsumerConfig{
					StreamNamePrefix:  "different_stream_{pod}",
					Type:              "push",
					CountPerStream:    1,
					DurableNamePrefix: "test_consumer_{pod}",
					AckWaitSeconds:    30,
					MaxAckPending:     1000,
					AckPolicy:         "explicit",
				},
				Behavior: BehaviorConfig{
					DurationSeconds: 300,
					RampUpSeconds:   10,
				},
			},
			wantError: true,
			errorMsg:  "publisher stream_name_prefix 'test_stream_0' must match consumer stream_name_prefix 'different_stream_0'",
		},
		{
			name: "static subject format is valid",
			spec: &LoadTestSpec{
				Name:           "test",
				NATSURL:        "nats://localhost:4222",
				ClientIDPrefix: "test_{pod}",
				Streams: []StreamSpec{
					{
						NamePrefix:                 "test_stream_{pod}",
						Count:                      1,
						Replicas:                   1,
						Subjects:                   []string{"test.subject.static"},
						MessagesPerStreamPerSecond: 100,
					},
				},
				Publishers: PublisherConfig{
					StreamNamePrefix:     "test_stream_{pod}",
					CountPerStream:       1,
					PublishRatePerSecond: 100,
					PublishPattern:       PublishPatternSteady,
					PublishBurstSize:     1,
					MessageSizeBytes:     256,
					TrackLatency:         false,
				},
				Consumers: ConsumerConfig{
					StreamNamePrefix:  "test_stream_{pod}",
					Type:              "push",
					CountPerStream:    1,
					DurableNamePrefix: "test_consumer_{pod}",
					AckWaitSeconds:    30,
					MaxAckPending:     1000,
					AckPolicy:         "explicit",
				},
				Behavior: BehaviorConfig{
					DurationSeconds: 300,
					RampUpSeconds:   10,
				},
			},
			wantError: false,
		},
		{
			name: "curly brace placeholder gets normalized",
			spec: &LoadTestSpec{
				Name:           "test",
				NATSURL:        "nats://localhost:4222",
				ClientIDPrefix: "test_{pod}",
				Streams: []StreamSpec{
					{
						NamePrefix:                 "test_stream_{pod}",
						Count:                      1,
						Replicas:                   1,
						Subjects:                   []string{"test.subject.{}"},
						MessagesPerStreamPerSecond: 100,
					},
				},
				Publishers: PublisherConfig{
					StreamNamePrefix:     "test_stream_{pod}",
					CountPerStream:       1,
					PublishRatePerSecond: 100,
					PublishPattern:       PublishPatternSteady,
					PublishBurstSize:     1,
					MessageSizeBytes:     256,
					TrackLatency:         false,
				},
				Consumers: ConsumerConfig{
					StreamNamePrefix:  "test_stream_{pod}",
					Type:              "push",
					CountPerStream:    1,
					DurableNamePrefix: "test_consumer_{pod}",
					AckWaitSeconds:    30,
					MaxAckPending:     1000,
					AckPolicy:         "explicit",
				},
				Behavior: BehaviorConfig{
					DurationSeconds: 300,
					RampUpSeconds:   10,
				},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.Validate()

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
			}

			// If validation passed and we had curly braces, check they were normalized
			if !tt.wantError && len(tt.spec.Streams) > 0 && len(tt.spec.Streams[0].Subjects) > 0 {
				subject := tt.spec.Streams[0].Subjects[0]
				if len(subject) >= 2 && subject[len(subject)-2:] == "{}" {
					t.Errorf("curly brace placeholder was not normalized to %%d")
				}
			}
		})
	}
}

func TestStreamConfigHelperMethods(t *testing.T) {
	t.Setenv("POD_NAME", "0")
	tests := []struct {
		name      string
		stream    StreamSpec
		testCases []struct {
			subjectIndex int32
			streamIndex  int
			expected     string
		}
		expectedAllSubjects []string
		streamIndexForAll   int
	}{
		{
			name: "subjects with format placeholders",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}", "test.other.{}"},
				MessagesPerStreamPerSecond: 100,
			},
			testCases: []struct {
				subjectIndex int32
				streamIndex  int
				expected     string
			}{
				{0, 5, "test.subject.5"},
				{1, 3, "test.other.3"},
			},
			expectedAllSubjects: []string{"test.subject.7", "test.other.7"},
			streamIndexForAll:   7,
		},
		{
			name: "static subjects without placeholders",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"static.subject", "another.static"},
				MessagesPerStreamPerSecond: 100,
			},
			testCases: []struct {
				subjectIndex int32
				streamIndex  int
				expected     string
			}{
				{0, 5, "static.subject"},
				{1, 3, "another.static"},
			},
			expectedAllSubjects: []string{"static.subject", "another.static"},
			streamIndexForAll:   7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.stream.Validate(); err != nil {
				t.Fatalf("unexpected stream validation error: %v", err)
			}

			for _, tc := range tt.testCases {
				result := tt.stream.FormatSubject(tc.subjectIndex, int32(tc.streamIndex))
				if result != tc.expected {
					t.Errorf("FormatSubject(%d, %d) = '%s', expected '%s'", tc.subjectIndex, tc.streamIndex, result, tc.expected)
				}
			}

			subjects := tt.stream.GetFormattedSubjects(int32(tt.streamIndexForAll))
			if len(subjects) != len(tt.expectedAllSubjects) {
				t.Errorf("expected %d subjects, got %d", len(tt.expectedAllSubjects), len(subjects))
				return
			}

			for i, expectedSubject := range tt.expectedAllSubjects {
				if subjects[i] != expectedSubject {
					t.Errorf("expected subject %d to be '%s', got '%s'", i, expectedSubject, subjects[i])
				}
			}
		})
	}
}

func TestStreamConfigValidation(t *testing.T) {
	t.Setenv("POD_NAME", "0")
	tests := []struct {
		name      string
		stream    StreamSpec
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid stream with all configurable options",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}"},
				MessagesPerStreamPerSecond: 100,
				Retention:                  RetentionLimits,
				MaxAge:                     "30m",
				Storage:                    StorageFile,
				DiscardNewPerSubject:       &[]bool{false}[0], // pointer to false
				Discard:                    DiscardNew,
				MaxMsgs:                    &[]int64{1000}[0],
				MaxBytes:                   &[]int64{1024 * 1024}[0], // 1MB
				MaxMsgsPerSubject:          &[]int64{100}[0],
				MaxConsumers:               &[]int{10}[0],
			},
			wantError: false,
		},
		{
			name: "valid stream with defaults (empty optional fields)",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}"},
				MessagesPerStreamPerSecond: 100,
			},
			wantError: false,
		},
		{
			name: "invalid retention policy",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}"},
				MessagesPerStreamPerSecond: 100,
				Retention:                  "invalid_retention",
			},
			wantError: true,
			errorMsg:  "retention must be",
		},
		{
			name: "invalid storage type",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}"},
				MessagesPerStreamPerSecond: 100,
				Storage:                    "invalid_storage",
			},
			wantError: true,
			errorMsg:  "storage must be",
		},
		{
			name: "invalid discard policy",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}"},
				MessagesPerStreamPerSecond: 100,
				Discard:                    "invalid_discard",
			},
			wantError: true,
			errorMsg:  "discard must be",
		},
		{
			name: "invalid max_age duration",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}"},
				MessagesPerStreamPerSecond: 100,
				MaxAge:                     "invalid_duration",
			},
			wantError: true,
			errorMsg:  "max_age must be a valid duration string",
		},
		{
			name: "valid complex duration formats",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}"},
				MessagesPerStreamPerSecond: 100,
				MaxAge:                     "1h30m45s",
			},
			wantError: false,
		},
		{
			name: "invalid max_msgs (too negative)",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}"},
				MessagesPerStreamPerSecond: 100,
				MaxMsgs:                    &[]int64{-5}[0],
			},
			wantError: true,
			errorMsg:  "max_msgs must be -1 (unlimited) or non-negative",
		},
		{
			name: "invalid max_bytes (too negative)",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}"},
				MessagesPerStreamPerSecond: 100,
				MaxBytes:                   &[]int64{-10}[0],
			},
			wantError: true,
			errorMsg:  "max_bytes must be -1 (unlimited) or non-negative",
		},
		{
			name: "valid unlimited values (-1)",
			stream: StreamSpec{
				NamePrefix:                 "test_stream_{pod}",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}"},
				MessagesPerStreamPerSecond: 100,
				MaxMsgs:                    &[]int64{-1}[0],
				MaxBytes:                   &[]int64{-1}[0],
				MaxMsgsPerSubject:          &[]int64{-1}[0],
				MaxConsumers:               &[]int{-1}[0],
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.stream.Validate()

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got none")
					return
				}
				if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing '%s', got '%s'", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestStreamSpecGetterMethods(t *testing.T) {
	t.Setenv("POD_NAME", "0")
	t.Run("GetRetentionPolicy", func(t *testing.T) {
		tests := []struct {
			name     string
			stream   StreamSpec
			expected nats.RetentionPolicy
		}{
			{
				name:     "limits policy",
				stream:   StreamSpec{Retention: RetentionLimits},
				expected: nats.LimitsPolicy,
			},
			{
				name:     "interest policy",
				stream:   StreamSpec{Retention: RetentionInterest},
				expected: nats.InterestPolicy,
			},
			{
				name:     "workqueue policy",
				stream:   StreamSpec{Retention: RetentionWorkQueue},
				expected: nats.WorkQueuePolicy,
			},
			{
				name:     "empty defaults to limits",
				stream:   StreamSpec{Retention: ""},
				expected: nats.LimitsPolicy,
			},
			{
				name:     "invalid defaults to limits",
				stream:   StreamSpec{Retention: "invalid"},
				expected: nats.LimitsPolicy,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := tt.stream.GetRetentionPolicy()
				if result != tt.expected {
					t.Errorf("GetRetentionPolicy() = %v, expected %v", result, tt.expected)
				}
			})
		}
	})

	t.Run("GetStorageType", func(t *testing.T) {
		tests := []struct {
			name     string
			stream   StreamSpec
			expected nats.StorageType
		}{
			{
				name:     "file storage",
				stream:   StreamSpec{Storage: StorageFile},
				expected: nats.FileStorage,
			},
			{
				name:     "memory storage",
				stream:   StreamSpec{Storage: StorageMemory},
				expected: nats.MemoryStorage,
			},
			{
				name:     "empty defaults to memory",
				stream:   StreamSpec{Storage: ""},
				expected: nats.MemoryStorage,
			},
			{
				name:     "invalid defaults to memory",
				stream:   StreamSpec{Storage: "invalid"},
				expected: nats.MemoryStorage,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := tt.stream.GetStorageType()
				if result != tt.expected {
					t.Errorf("GetStorageType() = %v, expected %v", result, tt.expected)
				}
			})
		}
	})

	t.Run("GetDiscardPolicy", func(t *testing.T) {
		tests := []struct {
			name     string
			stream   StreamSpec
			expected nats.DiscardPolicy
		}{
			{
				name:     "discard old",
				stream:   StreamSpec{Discard: DiscardOld},
				expected: nats.DiscardOld,
			},
			{
				name:     "discard new",
				stream:   StreamSpec{Discard: DiscardNew},
				expected: nats.DiscardNew,
			},
			{
				name:     "empty defaults to old",
				stream:   StreamSpec{Discard: ""},
				expected: nats.DiscardOld,
			},
			{
				name:     "invalid defaults to old",
				stream:   StreamSpec{Discard: "invalid"},
				expected: nats.DiscardOld,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := tt.stream.GetDiscardPolicy()
				if result != tt.expected {
					t.Errorf("GetDiscardPolicy() = %v, expected %v", result, tt.expected)
				}
			})
		}
	})

	t.Run("GetMaxAge", func(t *testing.T) {
		tests := []struct {
			name     string
			stream   StreamSpec
			expected time.Duration
		}{
			{
				name:     "valid duration",
				stream:   StreamSpec{MaxAge: "30m"},
				expected: 30 * time.Minute,
			},
			{
				name:     "complex duration",
				stream:   StreamSpec{MaxAge: "1h30m45s"},
				expected: 1*time.Hour + 30*time.Minute + 45*time.Second,
			},
			{
				name:     "empty defaults to 1 minute",
				stream:   StreamSpec{MaxAge: ""},
				expected: 1 * time.Minute,
			},
			{
				name:     "invalid defaults to 1 minute",
				stream:   StreamSpec{MaxAge: "invalid"},
				expected: 1 * time.Minute,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := tt.stream.GetMaxAge()
				if result != tt.expected {
					t.Errorf("GetMaxAge() = %v, expected %v", result, tt.expected)
				}
			})
		}
	})

	t.Run("GetDiscardNewPerSubject", func(t *testing.T) {
		tests := []struct {
			name     string
			stream   StreamSpec
			expected bool
		}{
			{
				name:     "nil defaults to false",
				stream:   StreamSpec{DiscardNewPerSubject: nil},
				expected: false,
			},
			{
				name:     "explicit true",
				stream:   StreamSpec{DiscardNewPerSubject: &[]bool{true}[0]},
				expected: true,
			},
			{
				name:     "explicit false",
				stream:   StreamSpec{DiscardNewPerSubject: &[]bool{false}[0]},
				expected: false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := tt.stream.GetDiscardNewPerSubject()
				if result != tt.expected {
					t.Errorf("GetDiscardNewPerSubject() = %v, expected %v", result, tt.expected)
				}
			})
		}
	})

	t.Run("GetMaxMsgs", func(t *testing.T) {
		tests := []struct {
			name     string
			stream   StreamSpec
			expected int64
		}{
			{
				name:     "nil defaults to -1",
				stream:   StreamSpec{MaxMsgs: nil},
				expected: -1,
			},
			{
				name:     "explicit value",
				stream:   StreamSpec{MaxMsgs: &[]int64{1000}[0]},
				expected: 1000,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := tt.stream.GetMaxMsgs()
				if result != tt.expected {
					t.Errorf("GetMaxMsgs() = %v, expected %v", result, tt.expected)
				}
			})
		}
	})

	t.Run("GetMaxBytes", func(t *testing.T) {
		tests := []struct {
			name     string
			stream   StreamSpec
			expected int64
		}{
			{
				name:     "nil defaults to -1",
				stream:   StreamSpec{MaxBytes: nil},
				expected: -1,
			},
			{
				name:     "explicit value",
				stream:   StreamSpec{MaxBytes: &[]int64{1024 * 1024}[0]}, // 1MB
				expected: 1024 * 1024,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := tt.stream.GetMaxBytes()
				if result != tt.expected {
					t.Errorf("GetMaxBytes() = %v, expected %v", result, tt.expected)
				}
			})
		}
	})

	t.Run("GetMaxMsgsPerSubject", func(t *testing.T) {
		tests := []struct {
			name     string
			stream   StreamSpec
			expected int64
		}{
			{
				name:     "nil defaults to -1",
				stream:   StreamSpec{MaxMsgsPerSubject: nil},
				expected: -1,
			},
			{
				name:     "explicit value",
				stream:   StreamSpec{MaxMsgsPerSubject: &[]int64{500}[0]},
				expected: 500,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := tt.stream.GetMaxMsgsPerSubject()
				if result != tt.expected {
					t.Errorf("GetMaxMsgsPerSubject() = %v, expected %v", result, tt.expected)
				}
			})
		}
	})

	t.Run("GetMaxConsumers", func(t *testing.T) {
		tests := []struct {
			name     string
			stream   StreamSpec
			expected int
		}{
			{
				name:     "nil defaults to -1",
				stream:   StreamSpec{MaxConsumers: nil},
				expected: -1,
			},
			{
				name:     "explicit value",
				stream:   StreamSpec{MaxConsumers: &[]int{10}[0]},
				expected: 10,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				result := tt.stream.GetMaxConsumers()
				if result != tt.expected {
					t.Errorf("GetMaxConsumers() = %v, expected %v", result, tt.expected)
				}
			})
		}
	})
}
