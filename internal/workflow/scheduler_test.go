package workflow

import (
	"testing"
	"time"
)

func TestScheduler_CronParsing(t *testing.T) {
	// Reference date: Friday, May 22, 2026 14:02:00
	from := time.Date(2026, 5, 22, 14, 2, 0, 0, time.UTC)

	tests := []struct {
		expr     string
		expected time.Time
	}{
		{
			// Every minute
			expr:     "* * * * *",
			expected: time.Date(2026, 5, 22, 14, 3, 0, 0, time.UTC),
		},
		{
			// Every 5 minutes
			expr:     "*/5 * * * *",
			expected: time.Date(2026, 5, 22, 14, 5, 0, 0, time.UTC),
		},
		{
			// Specific minute and hour (e.g. 15:30)
			expr:     "30 15 * * *",
			expected: time.Date(2026, 5, 22, 15, 30, 0, 0, time.UTC),
		},
		{
			// Range of minutes (e.g. 0-5)
			expr:     "0-5 * * * *",
			expected: time.Date(2026, 5, 22, 14, 3, 0, 0, time.UTC),
		},
		{
			// Day of week match: 6 is Saturday. So the next day (May 23, 2026) at 00:00
			expr:     "0 0 * * 6",
			expected: time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range tests {
		result := ParseCronNext(tc.expr, from)
		if !result.Equal(tc.expected) {
			t.Errorf("For cron %q: expected %s, got %s", tc.expr, tc.expected.Format(time.RFC3339), result.Format(time.RFC3339))
		}
	}
}
