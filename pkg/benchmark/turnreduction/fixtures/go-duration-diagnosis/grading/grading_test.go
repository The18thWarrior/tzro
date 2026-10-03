package timeutil

import (
	"testing"
	"time"
)

// Private grading test for the duration diagnosis fixture.
func TestGrading_SecondsWithHours(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{time.Hour + 30*time.Second, "1h 30s"},
		{2*time.Hour + 45*time.Second, "2h 45s"},
		{time.Hour + 30*time.Minute + 15*time.Second, "1h 30m 15s"},
		{3*time.Hour + 1*time.Second, "3h 1s"},
	}
	for _, tt := range tests {
		got := FormatDuration(tt.d)
		if got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestGrading_EdgeCases(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{999 * time.Millisecond, "0s"}, // sub-second rounds to 0
		{59 * time.Second, "59s"},
		{60 * time.Second, "1m"},
		{61 * time.Second, "1m 1s"},
	}
	for _, tt := range tests {
		got := FormatDuration(tt.d)
		if got != tt.want {
			t.Errorf("FormatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}
