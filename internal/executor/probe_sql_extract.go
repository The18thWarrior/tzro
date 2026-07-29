package executor

import "regexp"

// sqlFromCacheRe matches SELECT statements targeting cache_* tables.
// Used by Analyze Nodes to auto-extract SQL from model reasoning text
// when the model fails to emit an <ACTION> tag. (ADR-0058)
var sqlFromCacheRe = regexp.MustCompile(`(?i)(SELECT\s+.+?\s+FROM\s+(cache_\d{10,})(?:\s+(?:WHERE|GROUP|ORDER|LIMIT|HAVING|UNION)[^;]*?)?)(?:;|\n\n|$)`)

// extractSQLFromText attempts to find a SQL SELECT statement targeting a
// cache_* table in the given text. Returns (sql, cacheTable) on match,
// or ("", "") if no valid SQL is found.
//
// Scoped to Analyze Nodes only — generic Probe Nodes have arbitrary tool
// surfaces where auto-extraction would be fragile. The cache_\d{10,} pattern
// ensures we only match ephemeral query database tables, not arbitrary SQL.
func extractSQLFromText(text string) (string, string) {
	matches := sqlFromCacheRe.FindStringSubmatch(text)
	if len(matches) >= 3 {
		return matches[1], matches[2]
	}
	return "", ""
}
