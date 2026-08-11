package executor

import (
	"testing"
)

func TestDefaultSQLFallback_CacheIdPresent(t *testing.T) {
	sql := defaultSQLForCacheId("cache_1785202015624")
	expected := "SELECT * FROM cache_1785202015624 LIMIT 5"
	if sql != expected {
		t.Errorf("expected %q, got %q", expected, sql)
	}
}

func TestDefaultSQLFallback_EmptyCacheId(t *testing.T) {
	sql := defaultSQLForCacheId("")
	if sql != "" {
		t.Errorf("expected empty string for empty cacheId, got %q", sql)
	}
}

func TestExtractCacheIdFromText(t *testing.T) {
	t.Run("extracts cache ID from reasoning text", func(t *testing.T) {
		text := "I need to query the cached data from cache_1785202015624 to find the results"
		cacheId := extractCacheIdFromText(text)
		if cacheId != "cache_1785202015624" {
			t.Errorf("expected cache_1785202015624, got %q", cacheId)
		}
	})

	t.Run("returns empty for no cache ID", func(t *testing.T) {
		text := "There is no cached data available in this context"
		cacheId := extractCacheIdFromText(text)
		if cacheId != "" {
			t.Errorf("expected empty, got %q", cacheId)
		}
	})
}
