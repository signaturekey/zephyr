package dedupe

import (
	"testing"

	"github.com/signaturekey/zephyr/internal/schema"
)

func TestGroupFindingsMergesSameRootCause(t *testing.T) {
	left := finding("golang-expert-001", "golang-expert", schema.SeverityP1, 0.8, 10, 12)
	right := finding("code-reviewer-001", "code-reviewer", schema.SeverityP1, 0.9, 12, 14)

	groups := GroupFindings([]schema.CandidateFinding{left, right})
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if groups[0].Canonical.ID != right.ID {
		t.Fatalf("canonical = %q, want higher confidence %q", groups[0].Canonical.ID, right.ID)
	}
	if len(groups[0].SourceRoles) != 2 || len(groups[0].DuplicateIDs) != 1 {
		t.Fatalf("unexpected group metadata: %#v", groups[0])
	}
}

func TestGroupFindingsKeepsDifferentImpactSeparate(t *testing.T) {
	left := finding("a-001", "a", schema.SeverityP2, 0.8, 10, 10)
	right := finding("b-001", "b", schema.SeverityP2, 0.8, 10, 10)
	right.Impact = "different observable impact"
	if groups := GroupFindings([]schema.CandidateFinding{left, right}); len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
}

func TestGroupFindingsKeepsDisjointLocationsSeparate(t *testing.T) {
	left := finding("a-001", "a", schema.SeverityP2, 0.8, 10, 11)
	right := finding("b-001", "b", schema.SeverityP2, 0.8, 20, 21)
	if groups := GroupFindings([]schema.CandidateFinding{left, right}); len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
}

func finding(id, role string, severity schema.Severity, confidence float64, start, end int) schema.CandidateFinding {
	return schema.CandidateFinding{
		ID:         id,
		Role:       role,
		Severity:   severity,
		Category:   "context-propagation",
		Location:   schema.FindingLocation{File: "handler.go", LineStart: start, LineEnd: end},
		Evidence:   schema.FindingEvidence{ExecutionPath: "handler -> client", ViolatedInvariant: "cancellation propagates"},
		Impact:     "request continues after cancellation",
		Confidence: confidence,
	}
}
