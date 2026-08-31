package store

import "testing"

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"foo", "foo", true},
		{"foo", "bar", false},
		{"foo", "foobar", false},
		{"foo*", "foobar", true},
		{"foo*", "foo", true},
		{"*bar", "foobar", true},
		{"*bar", "bar", true},
		{"f*o", "fo", true},
		{"f*o", "faro", true},
		{"f*o", "fXo", true},
		{"f?o", "foo", true},
		{"f?o", "fXo", true},
		{"f?o", "fo", false},
		{"f?o", "faro", false},
		{"[abc]", "a", true},
		{"[abc]", "b", true},
		{"[abc]", "d", false},
		{"[^abc]", "a", false},
		{"[^abc]", "d", true},
		{"[a-z]", "a", true},
		{"[a-z]", "z", true},
		{"[a-z]", "A", false},
		{"[a-z]", "1", false},
		{"[a-z0-9]", "5", true},
		{"[a-z0-9]", "_", false},
		{"\\*", "*", true},
		{"\\*", "x", false},
		{"foo\\?", "foo?", true},
		{"foo\\?", "fooX", false},
		{"[a-z]", "a", true},
		{"[a-z]", "z", true},
		{"[a-z]", "aa", false}, // one char
		{"[^]", "]", true},   // [^] = negated empty class → matches everything
		{"[^]", "a", true},
		{"[]", "]", false},   // [] = empty class → matches nothing
		{"[]", "x", false},
		// unmatched '[' treated as literal
		{"[abc", "[abc", true},
		{"[abc", "x", false},
		// trailing backslash
		{"foo\\", "foo\\", true},
		{"foo\\", "foo", false},
		// path-style keys
		{"*", "k/v/123", true},
		{"k*", "k/v/123", true},
		{"k/v/*", "k/v/123", true},
		{"k/v/*", "k/v/", true},
		{"k/v/*", "k/v/x/y", true}, // Redis * matches / too (no separator concept)
		// empty pattern
		{"", "", true},
		{"", "x", false},
		// real-world key patterns
		{"user:*", "user:1000", true},
		{"user:*", "user:", true},
		{"user:*", "admin:1000", false},
		{"session:*:data", "session:abc:data", true},
		{"session:*:data", "session:abc:data:extra", false},
		{"h?llo", "hello", true},
		{"h?llo", "hallo", true},
		{"h?llo", "hXllo", true},
		{"h?llo", "hllo", false},
		{"h?llo", "heello", false},
	}
	for _, tc := range tests {
		got := GlobMatch(tc.pattern, tc.name)
		if got != tc.want {
			t.Errorf("GlobMatch(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func FuzzGlobMatch(f *testing.F) {
	f.Add("foo", "bar")
	f.Add("*", "foobar")
	f.Add("f?o", "foo")
	f.Add("[a-z]", "m")
	f.Add("user:*", "user:123")
	f.Fuzz(func(t *testing.T, pat, name string) {
		// must not panic
		GlobMatch(pat, name)
	})
}

func TestGlobMatchEdgeCases(t *testing.T) {
	// backslash at end
	if !GlobMatch("foo\\", "foo\\") {
		t.Error("trailing backslash should match literal")
	}
	// escaped backslash
	if !GlobMatch("foo\\\\", "foo\\") {
		t.Error("foo\\\\ should match foo\\")
	}
	// range with chars
	if !GlobMatch("[a]", "a") {
		t.Error("[a] should match a")
	}
	if GlobMatch("[a]", "b") {
		t.Error("[a] should not match b")
	}
	// unmatched '[' - literal match
	if !GlobMatch("[abc", "[abc") {
		t.Error("unmatched [ treated as literal")
	}
	// multiple stars
	if !GlobMatch("a*b*c", "aXbYc") {
		t.Error("a*b*c should match aXbYc")
	}
	// star at start
	if !GlobMatch("*a", "xa") {
		t.Error("*a should match xa")
	}
	if !GlobMatch("*a", "a") {
		t.Error("*a should match a")
	}
}