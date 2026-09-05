package evidence

import (
	"testing"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrecheckAcceptsConcreteChangedLine(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	require.NoError(t, err)
	code := "return newValue"
	finding := protocol.CandidateFinding{
		ID: "code-reviewer-001", Role: "code-reviewer", Severity: protocol.SeverityP1, Category: "correctness", Title: "broken value",
		Location: protocol.FindingLocation{File: "handler.go", LineStart: 2},
		Evidence: protocol.FindingEvidence{Code: &code, ExecutionPath: "handler returns the changed value", ViolatedInvariant: "value must remain valid", FalsifierChecked: "caller does not sanitize it"},
		Impact:   "request fails", Recommendation: "return a valid value", Confidence: 0.9,
	}
	diff := "diff --git a/handler.go b/handler.go\n--- a/handler.go\n+++ b/handler.go\n@@ -1,2 +1,2 @@\n package demo\n-return oldValue\n+return newValue\n"
	report := Precheck(protocol.CandidateEnvelope{Version: 1, RunID: "run", Role: "code-reviewer", Findings: []protocol.CandidateFinding{finding}}, Scope{
		RunID: "run", Diff: diff, ChangedFiles: []string{"handler.go"}, Config: cfg,
	})
	assert.Len(t, report.Accepted, 1)
	assert.Empty(t, report.Rejected)
}

func TestPrecheckAcceptsHighSeverityEvidenceFromDeletedCode(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	require.NoError(t, err)
	code := "if divisor == 0 { return 0 }"
	finding := protocol.CandidateFinding{
		ID: "code-reviewer-001", Role: "code-reviewer", Severity: protocol.SeverityP1, Category: "correctness", Title: "removed division guard",
		Location: protocol.FindingLocation{File: "calc.go", LineStart: 1},
		Evidence: protocol.FindingEvidence{Code: &code, ExecutionPath: "division receives an unchecked divisor", ViolatedInvariant: "division by zero must be prevented", FalsifierChecked: "callers may pass zero"},
		Impact:   "request panics", Recommendation: "restore the guard", Confidence: 0.9,
	}
	diff := "diff --git a/calc.go b/calc.go\n--- a/calc.go\n+++ b/calc.go\n@@ -1,2 +1 @@\n-if divisor == 0 { return 0 }\n return total / divisor\n"
	report := Precheck(protocol.CandidateEnvelope{Version: 1, RunID: "run", Role: "code-reviewer", Findings: []protocol.CandidateFinding{finding}}, Scope{
		RunID: "run", Diff: diff, ChangedFiles: []string{"calc.go"}, Config: cfg,
	})

	assert.Len(t, report.Accepted, 1)
	assert.Empty(t, report.Rejected)
}

func TestPrecheckRejectsLocationOutsideDiff(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	require.NoError(t, err)
	finding := protocol.CandidateFinding{
		ID: "code-reviewer-001", Role: "code-reviewer", Severity: protocol.SeverityP2, Category: "correctness", Title: "claim",
		Location: protocol.FindingLocation{File: "other.go", LineStart: 1},
		Evidence: protocol.FindingEvidence{ExecutionPath: "path", ViolatedInvariant: "invariant", FalsifierChecked: "checked"},
		Impact:   "impact", Recommendation: "fix", Confidence: 0.5,
	}
	report := Precheck(protocol.CandidateEnvelope{Version: 1, RunID: "run", Role: "code-reviewer", Findings: []protocol.CandidateFinding{finding}}, Scope{
		RunID: "run", Diff: "", ChangedFiles: []string{"handler.go"}, Config: cfg,
	})
	require.Len(t, report.Rejected, 1)
	assert.Equal(t, "out-of-scope", report.Rejected[0].ReasonCode)
}
