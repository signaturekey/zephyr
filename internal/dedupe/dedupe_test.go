package dedupe

import (
	"testing"

	"github.com/signaturekey/zephyr/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupFindingsMergesSameRootCause(t *testing.T) {
	left := finding("golang-expert-001", "golang-expert", protocol.SeverityP1, 0.8, 10, 12)
	right := finding("code-reviewer-001", "code-reviewer", protocol.SeverityP1, 0.9, 12, 14)

	groups := GroupFindings([]protocol.CandidateFinding{left, right})
	require.Len(t, groups, 1)
	assert.Equal(t, right.ID, groups[0].Canonical.ID)
	assert.Len(t, groups[0].SourceRoles, 2)
	assert.Len(t, groups[0].DuplicateIDs, 1)
}

func TestGroupFindingsKeepsDifferentImpactSeparate(t *testing.T) {
	left := finding("a-001", "a", protocol.SeverityP2, 0.8, 10, 10)
	right := finding("b-001", "b", protocol.SeverityP2, 0.8, 10, 10)
	right.Impact = "different observable impact"
	assert.Len(t, GroupFindings([]protocol.CandidateFinding{left, right}), 2)
}

func TestGroupFindingsKeepsDisjointLocationsSeparate(t *testing.T) {
	left := finding("a-001", "a", protocol.SeverityP2, 0.8, 10, 11)
	right := finding("b-001", "b", protocol.SeverityP2, 0.8, 20, 21)
	assert.Len(t, GroupFindings([]protocol.CandidateFinding{left, right}), 2)
}

func finding(id, role string, severity protocol.Severity, confidence float64, start, end int) protocol.CandidateFinding {
	return protocol.CandidateFinding{
		ID:         id,
		Role:       role,
		Severity:   severity,
		Category:   "context-propagation",
		Location:   protocol.FindingLocation{File: "handler.go", LineStart: start, LineEnd: end},
		Evidence:   protocol.FindingEvidence{ExecutionPath: "handler -> client", ViolatedInvariant: "cancellation propagates"},
		Impact:     "request continues after cancellation",
		Confidence: confidence,
	}
}
