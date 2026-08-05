package executor

import (
	"testing"
)

func TestCoerceStringArguments_CacheIdBypass(t *testing.T) {
	t.Run("preserves cache_ prefixed cacheId values even when not in instruction", func(t *testing.T) {
		args := map[string]interface{}{
			"cacheId": "cache_1785202015624",
		}
		// The instruction doesn't contain the cacheId AND doesn't contain
		// any string values that could be matched — pure coercion scenario.
		instruction := "Query the data to find distribution patterns"

		coerceStringArguments(args, instruction, "introspect_cache")

		if args["cacheId"] != "cache_1785202015624" {
			t.Errorf("cacheId was coerced to %q, expected it to be preserved as cache_1785202015624",
				args["cacheId"])
		}
	})

	t.Run("preserves cache_ prefixed sql cacheId values", func(t *testing.T) {
		args := map[string]interface{}{
			"cacheId": "cache_9876543210123",
			"sql":     "SELECT * FROM cache_9876543210123 LIMIT 50",
		}
		instruction := "Run a SQL query on the cached data"

		coerceStringArguments(args, instruction, "sql_cached_data")

		if args["cacheId"] != "cache_9876543210123" {
			t.Errorf("cacheId was coerced to %q, expected preservation", args["cacheId"])
		}
	})
}
