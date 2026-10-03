package store

import (
	"fmt"
	"strings"
)

// validateSingleSelect prevents a SELECT-prefixed second statement from reaching
// the driver. Semicolons inside SQL quotes and comments are data, not separators.
func validateSingleSelect(query string) error {
	if strings.IndexByte(query, 0) >= 0 {
		return fmt.Errorf("query must not contain NUL")
	}
	skipTrivia := func(start int) (int, error) {
		i := start
		for i < len(query) {
			switch query[i] {
			case ' ', '\t', '\n', '\r', '\v', '\f':
				i++
				continue
			}
			if strings.HasPrefix(query[i:], "--") {
				end := strings.IndexByte(query[i+2:], '\n')
				if end < 0 {
					return len(query), nil
				}
				i += end + 3
				continue
			}
			if strings.HasPrefix(query[i:], "/*") {
				end := strings.Index(query[i+2:], "*/")
				if end < 0 {
					return 0, fmt.Errorf("unterminated SQL comment")
				}
				i += end + 4
				continue
			}
			break
		}
		return i, nil
	}
	start, err := skipTrivia(0)
	if err != nil {
		return err
	}
	end := start
	for end < len(query) && ((query[end] >= 'a' && query[end] <= 'z') || (query[end] >= 'A' && query[end] <= 'Z')) {
		end++
	}
	if !strings.EqualFold(query[start:end], "SELECT") {
		return fmt.Errorf("only one SELECT query is allowed")
	}
	terminated := false
	for i := end; i < len(query); {
		i, err = skipTrivia(i)
		if err != nil {
			return err
		}
		if i == len(query) {
			break
		}
		if terminated {
			return fmt.Errorf("only one SELECT query is allowed; remove appended statements")
		}
		if query[i] == ';' {
			terminated = true
			i++
			continue
		}
		quote := query[i]
		if quote != '\'' && quote != '"' && quote != '`' && quote != '[' {
			i++
			continue
		}
		close := quote
		if quote == '[' {
			close = ']'
		}
		i++
		closed := false
		for i < len(query) {
			if query[i] != close {
				i++
				continue
			}
			if quote != '[' && i+1 < len(query) && query[i+1] == close {
				i += 2
				continue
			}
			i++
			closed = true
			break
		}
		if !closed {
			return fmt.Errorf("unterminated SQL quote")
		}
	}
	return nil
}
