// Package agent implements the mem-x agent meta-layer: deterministic task
// classification and routing (AGENTS.md §3) and the orchestrator's JSON-lines
// protocol (docs/agent-protocol.md).
//
// The classifier is a deterministic rules engine — it grades a task line into
// complexity/type/agent/model-tier so routing is reproducible and testable.
// Security and protocol tasks never route below tier 3 (AGENTS.md §3).
package agent

import (
	"fmt"
	"strings"
)

// Complexity is the graded task size (AGENTS.md §3).
type Complexity string

// TaskType is the graded kind of work (AGENTS.md §1 / agents/classifier.md).
type TaskType string

// ModelTier is the model-quality bar (AGENTS.md §3: 1..4).
type ModelTier int

const (
	Small      Complexity = "S"
	Medium     Complexity = "M"
	Large      Complexity = "L"
	ExtraLarge Complexity = "XL"
)

const (
	TypeDesign      TaskType = "design"
	TypeCode        TaskType = "code"
	TypeReview      TaskType = "review"
	TypeSecurity    TaskType = "security"
	TypeResearch    TaskType = "research"
	TypeBench       TaskType = "bench"
	TypeDocs        TaskType = "docs"
	TypePortability TaskType = "portability"
)

const (
	Tier1 ModelTier = 1 // fast/cheap
	Tier2 ModelTier = 2 // standard
	Tier3 ModelTier = 3 // strong
	Tier4 ModelTier = 4 // strongest
)

// minSecurityTier is the floor for security/protocol work (AGENTS.md §3).
const minSecurityTier = Tier3

// Registry maps a task type to its owning agent (AGENTS.md §1).
var Registry = map[TaskType]string{
	TypeDesign:      "planner",
	TypeCode:        "engineer",
	TypeReview:      "reviewer",
	TypeSecurity:    "security",
	TypeResearch:    "research",
	TypeBench:       "bench",
	TypeDocs:        "docs",
	TypePortability: "portability",
}

// Route is one classifier decision, formatted as a single routing line
// (agents/classifier.md output contract).
type Route struct {
	TaskID     string
	Complexity Complexity
	Type       TaskType
	Agent      string
	Model      ModelTier
	Reason     string
}

// Line renders the routing line in the classifier's output-contract format.
// The reason is kept single-token (no spaces) so the line stays parseable:
//
//	task=<id> complexity=<S|M|L|XL> type=<...> agent=<id> model=<tier> reason=<short>
func (r Route) Line() string {
	return fmt.Sprintf(
		"task=%s complexity=%s type=%s agent=%s model=%d reason=%s",
		r.TaskID, r.Complexity, r.Type, r.Agent, r.Model, r.Reason,
	)
}

// upWords raise complexity (risk amplifiers). One hit = +1 grade.
var upWords = []string{
	"concurr", "race", "deadlock", "goroutine", "mutex", "lock", "atomic",
	"protocol", "resp", "parser", "codec", "wire",
	"memor", "alloc", "leak", "unsafe", "pool",
	"secur", "cve", "vuln", "fuzz", "attack", "threat", "sanit", "limit", "cap",
	"distribut", "replica", "cluster", "shard",
}

// downWords lower complexity (mechanical/trivial work). One hit = −1 grade.
var downWords = []string{
	"typo", "spelling", "format", "comment", "rename", "docs", "document",
	"changelog", "readme", "version bump", "trivial", "mechanical", "lint",
}

// classifyType detects the dominant task type by keyword.
func classifyType(task string) (TaskType, string) {
	t := strings.ToLower(task)
	switch {
	case containsAny(t, "secur", "cve", "vuln", "fuzz", "attack", "threat", "sanit", "exploit", "abuse", "harden", "cap", "limit"):
		return TypeSecurity, "security-keywords"
	case containsAny(t, "design", "architect", "plan", "schema", "interface", "refactor toward", "roadmap"):
		return TypeDesign, "design-keywords"
	case containsAny(t, "review", "audit", "inspect", "check for"):
		return TypeReview, "review-keywords"
	case containsAny(t, "benchmark", "bench ", "measure", "ns/op", "allocs/op", "profile", "latency", "throughput"):
		return TypeBench, "measurement-keywords"
	case containsAny(t, "research", "cleverer", "pattern", "compare", "alternative", "survey", "literature", "how does"):
		return TypeResearch, "research-keywords"
	case containsAny(t, "cross", "windows", "darwin", "mac", "portability", "GOOS", "GOARCH", "static build", "release"):
		if containsAny(t, "build", "compile", "port", "matrix") {
			return TypePortability, "portability-keywords"
		}
		return TypeDocs, "release/portability-docs"
	case containsAny(t, "docs", "document", "readme", "changelog", "comment", "guideline", "help"):
		return TypeDocs, "docs-keywords"
	default:
		return TypeCode, "default-code"
	}
}

// gradeComplexity scores risk by keyword hits, clamped to S..XL. Base is 1
// (S); routine engineering types are promoted to M in Classify.
func gradeComplexity(task string) (Complexity, string) {
	t := strings.ToLower(task)
	up, down := 0, 0
	for _, w := range upWords {
		if strings.Contains(t, w) {
			up++
		}
	}
	for _, w := range downWords {
		if strings.Contains(t, w) {
			down++
		}
	}
	grade := 1 + up - down
	if grade < 1 {
		grade = 1
	}
	if grade > 4 {
		grade = 4
	}
	c := [...]Complexity{Small, Medium, Large, ExtraLarge}[grade-1]
	return c, fmt.Sprintf("risk=%d;up=%d;down=%d", grade, up, down)
}

// ModelFor maps complexity to a model tier (AGENTS.md §3).
func ModelFor(c Complexity) ModelTier {
	switch c {
	case Small:
		return Tier1
	case Medium:
		return Tier2
	case Large:
		return Tier3
	default:
		return Tier4
	}
}

// promotedTypes are engineering work kinds where a Small task is still real
// work and should be treated as Medium (routine professional work is never S).
var promotedTypes = map[TaskType]bool{
	TypeDesign: true, TypeCode: true, TypeReview: true,
	TypeBench: true, TypeResearch: true,
}

// Classify grades a free-text task and returns a Route. The task id is passed
// through so the caller owns id assignment; an empty id gets "0".
func Classify(taskID, task string) Route {
	typ, typeReason := classifyType(task)
	compl, gradeReason := gradeComplexity(task)
	if compl == Small && promotedTypes[typ] {
		compl = Medium
		gradeReason += ";promoted-routine-work"
	}
	model := ModelFor(compl)
	reason := typeReason + ";" + gradeReason

	// Hard floor: security and protocol work never routes below tier 3.
	if (typ == TypeSecurity || containsAny(strings.ToLower(task), "protocol", "resp", "parser", "codec")) && model < minSecurityTier {
		model = minSecurityTier
		reason += ";security/protocol-floor=>tier3"
	}
	if taskID == "" {
		taskID = "0"
	}
	return Route{TaskID: taskID, Complexity: compl, Type: typ, Agent: Registry[typ], Model: model, Reason: reason}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
