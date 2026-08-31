package store

// GlobMatch reports whether name matches the Redis-style glob pattern.
// Supported operators:
//   *         matches any sequence of characters (including empty)
//   ?         matches any single byte
//   [abc]     matches one byte in the set
//   [^abc]    matches one byte NOT in the set
//   [a-z]     range inside a char class
//   \x        escapes x (match it literally)
//
// A trailing backslash matches a literal backslash.  An unmatched '[' is
// treated as a literal (matching Redis behaviour).
func GlobMatch(pattern, name string) bool {
	return globMatch(pattern, name)
}

// globMatch implements the matching; the exported GlobMatch is the only entry
// point (tested independently, reused by Keys/Scan).
func globMatch(pattern, s string) bool {
	pi, si := 0, 0
	star := -1 // last '*' position in pattern
	mark := 0  // position in s to resume afteprintf 'SET k v\r\n' | nc 127.0.0.1 6379r '*'

	for si < len(s) {
		if pi < len(pattern) {
			c := pattern[pi]
			if c == '\\' && pi+1 < len(pattern) {
				pi++ // consume '\'
				if pattern[pi] == s[si] {
					pi++
					si++
					continue
				}
			} else if c == '?' {
				pi++
				si++
				continue
			} else if c == '[' {
				if ok, next := matchClass(pattern, pi, s[si]); ok {
					pi = next
					si++
					continue
				}
				// class did not match: '[' is a literal here
				if '[' == s[si] {
					pi++
					si++
					continue
				}
			} else if c == '*' {
				star = pi
				mark = si
				pi++
				continue
			} else if c == s[si] {
				pi++
				si++
				continue
			}
		}
		// mismatch: backtrack to last '*'
		if star >= 0 {
			pi = star + 1
			mark++
			si = mark
			continue
		}
		return false
	}
	// consume trailing '*'
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}

// matchClass parses a [...] character class starting at pattern[pos] using
// Redis semantics: ']' always closes the class, '^' as the first member
// negates it, '\x' escapes, and 'a-z' forms ranges. The class may be empty
// ('[]' matches nothing, '[^]' matches everything). Returns whether ch
// matches, and the position after the closing ']'. When the class never
// closes it returns ok=false and pos+1, so the caller treats '[' literally.
func matchClass(pattern string, pos int, ch byte) (bool, int) {
	if pos+1 >= len(pattern) {
		return false, pos + 1 // '[' at end → literal
	}
	i := pos + 1 // past '['
	negate := false
	if pattern[i] == '^' || pattern[i] == '!' {
		negate = true
		i++
	}
	matched := false
	for i < len(pattern) {
		c := pattern[i]
		if c == ']' {
			if negate {
				matched = !matched
			}
			return matched, i + 1
		}
		if c == '\\' && i+1 < len(pattern) {
			i++
			if ch == pattern[i] {
				matched = true
			}
			i++
			continue
		}
		// range: a-z (only when the '-' is not the last member before ']')
		if i+2 < len(pattern) && pattern[i+1] == '-' && pattern[i+2] != ']' {
			lo, hi := c, pattern[i+2]
			if ch >= lo && ch <= hi {
				matched = true
			}
			i += 3
			continue
		}
		if ch == c {
			matched = true
		}
		i++
	}
	// no closing ']' → '[' was literal; caller compares '[' itself
	return false, pos + 1
}