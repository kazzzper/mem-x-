package cli

import (
	"strings"
	"unicode"
)

// Tokenize splits a line into command tokens, respecting double-quoted
// strings and backslash escapes (matching redis-cli's tokenizer). A
// single-quote is treated as a literal character (not a quoting delimiter).
func Tokenize(line string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	escape := false
	for _, r := range line {
		if escape {
			cur.WriteRune(r)
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && unicode.IsSpace(r) {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}
