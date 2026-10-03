// Package timeutil provides human-readable duration formatting.
package timeutil

import "time"

// FormatDuration converts a time.Duration to a human-readable string.
// Examples: "2h 30m", "45m 10s", "5s", "0s".
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "-" + FormatDuration(-d)
	}

	totalMinutes := int(d.Minutes())
	hours := totalMinutes / 60
	minutes := totalMinutes % 60
	seconds := int(d.Seconds()) - totalMinutes*60

	result := ""
	if hours > 0 {
		result += intStr(hours) + "h"
	}
	if minutes > 0 {
		if result != "" {
			result += " "
		}
		result += intStr(minutes) + "m"
	}
	if seconds > 0 && hours == 0 {
		if result != "" {
			result += " "
		}
		result += intStr(seconds) + "s"
	}
	if result == "" {
		return "0s"
	}
	return result
}

func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
