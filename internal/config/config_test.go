package config

import (
	"strings"
	"testing"
)

func TestStreamSynchronizationValidation(t *testing.T) {
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
				ClientIDPrefix: "test",
				Streams: []StreamSpec{
					{
						NamePrefix:                 "test_stream",
						Count:                      1,
						Replicas:                   1,
						Subjects:                   []string{"test.subject.{}"},
						MessagesPerStreamPerSecond: 100,
						MessageSizeBytes:           256,
					},
				},
				Publishers: PublisherConfig{
					StreamNamePrefix:     "test_stream",
					CountPerStream:       1,
					PublishRatePerSecond: 100,
					PublishPattern:       PublishPatternSteady,
					PublishBurstSize:     1,
					MessageSizeBytes:     256,
					TrackLatency:         false,
				},
				Consumers: ConsumerConfig{
					StreamNamePrefix:  "test_stream",
					Type:              "push",
					CountPerStream:    1,
					DurableNamePrefix: "test_consumer",
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
				ClientIDPrefix: "test",
				Streams: []StreamSpec{
					{
						NamePrefix:                 "test_stream",
						Count:                      1,
						Replicas:                   1,
						Subjects:                   []string{"test.subject.{}"},
						MessagesPerStreamPerSecond: 100,
						MessageSizeBytes:           256,
					},
				},
				Publishers: PublisherConfig{
					StreamNamePrefix:     "different_stream",
					CountPerStream:       1,
					PublishRatePerSecond: 100,
					PublishPattern:       PublishPatternSteady,
					PublishBurstSize:     1,
					MessageSizeBytes:     256,
					TrackLatency:         false,
				},
				Consumers: ConsumerConfig{
					StreamNamePrefix:  "test_stream",
					Type:              "push",
					CountPerStream:    1,
					DurableNamePrefix: "test_consumer",
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
			errorMsg:  "publisher stream_name_prefix 'different_stream' must match consumer stream_name_prefix 'test_stream'",
		},
		{
			name: "consumer stream prefix mismatch",
			spec: &LoadTestSpec{
				Name:           "test",
				NATSURL:        "nats://localhost:4222",
				ClientIDPrefix: "test",
				Streams: []StreamSpec{
					{
						NamePrefix:                 "test_stream",
						Count:                      1,
						Replicas:                   1,
						Subjects:                   []string{"test.subject.{}"},
						MessagesPerStreamPerSecond: 100,
						MessageSizeBytes:           256,
					},
				},
				Publishers: PublisherConfig{
					StreamNamePrefix:     "test_stream",
					CountPerStream:       1,
					PublishRatePerSecond: 100,
					PublishPattern:       PublishPatternSteady,
					PublishBurstSize:     1,
					MessageSizeBytes:     256,
					TrackLatency:         false,
				},
				Consumers: ConsumerConfig{
					StreamNamePrefix:  "different_stream",
					Type:              "push",
					CountPerStream:    1,
					DurableNamePrefix: "test_consumer",
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
			errorMsg:  "publisher stream_name_prefix 'test_stream' must match consumer stream_name_prefix 'different_stream'",
		},
		{
			name: "static subject format is valid",
			spec: &LoadTestSpec{
				Name:           "test",
				NATSURL:        "nats://localhost:4222",
				ClientIDPrefix: "test",
				Streams: []StreamSpec{
					{
						NamePrefix:                 "test_stream",
						Count:                      1,
						Replicas:                   1,
						Subjects:                   []string{"test.subject.static"},
						MessagesPerStreamPerSecond: 100,
						MessageSizeBytes:           256,
					},
				},
				Publishers: PublisherConfig{
					StreamNamePrefix:     "test_stream",
					CountPerStream:       1,
					PublishRatePerSecond: 100,
					PublishPattern:       PublishPatternSteady,
					PublishBurstSize:     1,
					MessageSizeBytes:     256,
					TrackLatency:         false,
				},
				Consumers: ConsumerConfig{
					StreamNamePrefix:  "test_stream",
					Type:              "push",
					CountPerStream:    1,
					DurableNamePrefix: "test_consumer",
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
				ClientIDPrefix: "test",
				Streams: []StreamSpec{
					{
						NamePrefix:                 "test_stream",
						Count:                      1,
						Replicas:                   1,
						Subjects:                   []string{"test.subject.{}"},
						MessagesPerStreamPerSecond: 100,
						MessageSizeBytes:           256,
					},
				},
				Publishers: PublisherConfig{
					StreamNamePrefix:     "test_stream",
					CountPerStream:       1,
					PublishRatePerSecond: 100,
					PublishPattern:       PublishPatternSteady,
					PublishBurstSize:     1,
					MessageSizeBytes:     256,
					TrackLatency:         false,
				},
				Consumers: ConsumerConfig{
					StreamNamePrefix:  "test_stream",
					Type:              "push",
					CountPerStream:    1,
					DurableNamePrefix: "test_consumer",
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
				NamePrefix:                 "test_stream",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"test.subject.{}", "test.other.{}"},
				MessagesPerStreamPerSecond: 100,
				MessageSizeBytes:           256,
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
				NamePrefix:                 "test_stream",
				Count:                      1,
				Replicas:                   1,
				Subjects:                   []string{"static.subject", "another.static"},
				MessagesPerStreamPerSecond: 100,
				MessageSizeBytes:           256,
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
