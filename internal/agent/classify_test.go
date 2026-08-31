package agent

import (
	"strings"
	"testing"
)

func TestClassifyTypes(t *testing.T) {
	cases := []struct {
		task  string
		typ   TaskType
		agent string
	}{
		{"implement a new SET command handler in the dispatcher", TypeCode, "engineer"},
		{"design the storage engine architecture for TTL expiry", TypeDesign, "planner"},
		{"review the resp parser for correctness and growing patterns", TypeReview, "reviewer"},
		{"fuzz the parser and audit input caps for CVE risk", TypeSecurity, "security"},
		{"benchmark SET vs GET throughput and allocation", TypeBench, "bench"},
		{"research cleverer patterns used by dragonfly for eviction", TypeResearch, "research"},
		{"update the README and write the changelog", TypeDocs, "docs"},
		{"cross-compile and test the static builds on windows and darwin", TypePortability, "portability"},
		{"fix a typo in a comment", TypeDocs, "docs"},
	}
	for _, c := range cases {
		r := Classify("t1", c.task)
		if r.Type != c.typ {
			t.Errorf("Classify(%q).Type = %q, want %q", c.task, r.Type, c.typ)
		}
		if r.Agent != c.agent {
			t.Errorf("Classify(%q).Agent = %q, want %q", c.task, r.Agent, c.agent)
		}
	}
}

func TestComplexityGrades(t *testing.T) {
	cases := []struct {
		task string
		want Complexity
	}{
		{"fix a typo in a comment", Small},
		{"update the README", Small},
		{"implement a new command in the dispatcher", Medium},
		{"add a sharded map with locks", Large},
		{"redesign the parser protocol with memory safety and race safety", ExtraLarge},
	}
	for _, c := range cases {
		r := Classify("t", c.task)
		if r.Complexity != c.want {
			t.Errorf("Classify(%q).Complexity = %q, want %q", c.task, r.Complexity, c.want)
		}
	}
}

func TestSecurityProtocolFloor(t *testing.T) {
	// Security work never routes below tier 3, even for a short task string.
	for _, task := range []string{
		"review input caps",
		"quick fuzz run",
		"fix the resp parser edge case",
		"audit the codec for a protocol bug",
	} {
		r := Classify("t", task)
		if r.Model < minSecurityTier {
			t.Errorf("Classify(%q).Model = %d, want >= %d", task, r.Model, minSecurityTier)
		}
	}
}

func TestRegistryCompleteness(t *testing.T) {
	// Every task type has a registered agent; every agent is a real, distinct value.
	if len(Registry) != 8 {
		t.Errorf("Registry has %d types, want 8", len(Registry))
	}
	seen := map[string]bool{}
	for typ, agent := range Registry {
		if agent == "" {
			t.Errorf("type %q has empty agent", typ)
		}
		seen[agent] = true
	}
	// Classifier output must never reference an unregistered agent.
	for _, typ := range []TaskType{TypeDesign, TypeCode, TypeReview, TypeSecurity, TypeResearch, TypeBench, TypeDocs, TypePortability} {
		if _, ok := Registry[typ]; !ok {
			t.Errorf("type %q missing from registry", typ)
		}
	}
}

func TestRouteLineFormat(t *testing.T) {
	r := Classify("42", "implement a new command in the dispatcher")
	line := r.Line()
	for _, part := range []string{"task=42", "complexity=", "type=", "agent=", "model=", "reason="} {
		if !strings.Contains(line, part) {
			t.Errorf("routing line %q missing %q", line, part)
		}
	}
	// The routing line is exactly the six key=value fields, space-separated
	// (the reason value itself may contain "=", so count fields, not "=").
	fields := strings.Fields(line)
	if len(fields) != 6 {
		t.Errorf("routing line has %d fields, want 6: %q", len(fields), line)
	}
	want := []string{"task=42", "complexity=", "type=", "agent=", "model=", "reason="}
	for i, w := range want {
		if i < len(fields) && !strings.HasPrefix(fields[i], w) {
			t.Errorf("field %d = %q, want prefix %q (line: %q)", i, fields[i], w, line)
		}
	}
}

func TestEmptyTaskDefaultsToCode(t *testing.T) {
	r := Classify("", "implement the thing")
	if r.TaskID != "0" {
		t.Errorf("empty task id should default to 0, got %q", r.TaskID)
	}
	r2 := Classify("7", "")
	if r2.Type != TypeCode {
		t.Errorf("empty task should classify as code, got %q", r2.Type)
	}
}
