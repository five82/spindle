package logging

import "testing"

func TestNewProgressSampler(t *testing.T) {
	tests := []struct {
		name       string
		bucketSize float64
		wantSize   float64
	}{
		{"default bucket size for zero", 0, 5},
		{"default bucket size for negative", -1, 5},
		{"custom bucket size", 10, 10},
		{"small bucket size", 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewProgressSampler(tt.bucketSize)
			if s.bucketSize != tt.wantSize {
				t.Errorf("bucketSize = %v, want %v", s.bucketSize, tt.wantSize)
			}
			if s.lastBucket != -1 {
				t.Errorf("lastBucket = %d, want -1", s.lastBucket)
			}
		})
	}
}

func TestProgressSampler_NilSampler(t *testing.T) {
	var s *ProgressSampler
	if !s.ShouldLog(50, "stage", "message") {
		t.Error("ShouldLog on nil sampler should always return true")
	}
	s.Reset() // should not panic
}

func TestProgressSampler_ShouldLogStageChange(t *testing.T) {
	s := NewProgressSampler(5)

	// First stage should log
	if !s.ShouldLog(0, "Ripping", "starting") {
		t.Error("first stage should log")
	}

	// Same stage, same percent should not log
	if s.ShouldLog(0, "Ripping", "still starting") {
		t.Error("same stage and percent should not log again")
	}

	// Different stage should log
	if !s.ShouldLog(0, "Encoding", "starting") {
		t.Error("different stage should log")
	}

	// Verify stage was updated
	if s.lastStage != "Encoding" {
		t.Errorf("lastStage = %q, want Encoding", s.lastStage)
	}
}

func TestProgressSampler_ShouldLogStageTrimsWhitespace(t *testing.T) {
	s := NewProgressSampler(5)

	s.ShouldLog(0, "  Ripping  ", "starting")
	if s.lastStage != "Ripping" {
		t.Errorf("lastStage = %q, want Ripping (trimmed)", s.lastStage)
	}
}

func TestProgressSampler_ShouldLogPercentBuckets(t *testing.T) {
	s := NewProgressSampler(5) // 5% buckets

	// 0% should log (first call)
	if !s.ShouldLog(0, "Test", "") {
		t.Error("0% should log")
	}

	// 3% is still in bucket 0, should not log
	if s.ShouldLog(3, "Test", "") {
		t.Error("3% should not log (same bucket)")
	}

	// 5% crosses into bucket 1, should log
	if !s.ShouldLog(5, "Test", "") {
		t.Error("5% should log (new bucket)")
	}

	// 7% is still in bucket 1
	if s.ShouldLog(7, "Test", "") {
		t.Error("7% should not log (same bucket)")
	}

	// 10% crosses into bucket 2
	if !s.ShouldLog(10, "Test", "") {
		t.Error("10% should log (new bucket)")
	}
}

func TestProgressSampler_ShouldLogNegativePercent(t *testing.T) {
	s := NewProgressSampler(5)

	// First call with negative percent should still log (stage change)
	if !s.ShouldLog(-1, "Unknown", "") {
		t.Error("first call should log even with negative percent")
	}

	// Second call with same stage and negative percent should not log
	if s.ShouldLog(-1, "Unknown", "") {
		t.Error("negative percent should not trigger bucket logging")
	}
}

func TestProgressSampler_ShouldLogCaps100Percent(t *testing.T) {
	s := NewProgressSampler(5)

	// Jump to 95%
	s.ShouldLog(95, "Test", "")

	// 100% should log
	if !s.ShouldLog(100, "Test", "") {
		t.Error("100% should log")
	}

	// Values over 100% should use 100% bucket
	if s.ShouldLog(105, "Test", "") {
		t.Error("105% should not log again (same as 100% bucket)")
	}
}

func TestProgressSampler_ShouldLogBucketResetOnStageChange(t *testing.T) {
	s := NewProgressSampler(5)

	// Progress to 50%
	s.ShouldLog(50, "Ripping", "")

	// Change stage - bucket should reset
	s.ShouldLog(0, "Encoding", "")

	// Now 10% should log (bucket was reset to -1)
	if !s.ShouldLog(10, "Encoding", "") {
		t.Error("10% should log after stage change reset bucket")
	}
}

func TestProgressSampler_ShouldLogIgnoresMessage(t *testing.T) {
	s := NewProgressSampler(5)

	s.ShouldLog(10, "Test", "first message")

	// Different message but same stage/percent should not log
	if s.ShouldLog(10, "Test", "different message with ETA") {
		t.Error("different message should not trigger logging")
	}
}

func TestProgressSampler_Reset(t *testing.T) {
	s := NewProgressSampler(5)
	s.ShouldLog(50, "Ripping", "")

	s.Reset()

	if s.lastStage != "" {
		t.Errorf("lastStage = %q, want empty after reset", s.lastStage)
	}
	if s.lastBucket != -1 {
		t.Errorf("lastBucket = %d, want -1 after reset", s.lastBucket)
	}
	if !s.ShouldLog(50, "Ripping", "") {
		t.Error("should log after reset")
	}
}

func TestProgressSampler_BucketSizes(t *testing.T) {
	t.Run("1% buckets", func(t *testing.T) {
		s := NewProgressSampler(1)
		s.ShouldLog(0, "Test", "")

		if !s.ShouldLog(1, "Test", "") {
			t.Error("1% should log")
		}
		if s.ShouldLog(1.5, "Test", "") {
			t.Error("1.5% should not log (same bucket)")
		}
		if !s.ShouldLog(2, "Test", "") {
			t.Error("2% should log")
		}
	})

	t.Run("25% buckets", func(t *testing.T) {
		s := NewProgressSampler(25)
		s.ShouldLog(0, "Test", "")

		if s.ShouldLog(20, "Test", "") {
			t.Error("20% should not log")
		}
		if !s.ShouldLog(25, "Test", "") {
			t.Error("25% should log")
		}
		if s.ShouldLog(49, "Test", "") {
			t.Error("49% should not log")
		}
		if !s.ShouldLog(50, "Test", "") {
			t.Error("50% should log")
		}
	})
}
