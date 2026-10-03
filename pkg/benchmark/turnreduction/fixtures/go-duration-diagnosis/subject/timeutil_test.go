package timeutil

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"5 seconds", 5 * time.Second, "5s"},
		{"45 minutes", 45 * time.Minute, "45m"},
		{"2 hours", 2 * time.Hour, "2h"},
		{"1h 30m", time.Hour + 30*time.Minute, "1h 30m"},
		// This test case exposes the bug: seconds are dropped when hours > 0
		{"1h 0m 30s", time.Hour + 30*time.Second, "1h 30s"},
		{"negative", -(2*time.Minute + 30*time.Second), "-2m 30s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDuration(tt.d)
			if got != tt.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}
