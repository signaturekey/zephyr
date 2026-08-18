package report

import (
	"testing"
	"time"

	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/protocol"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAggregateAndRenderKeepsEverySeverity(t *testing.T) {
	findings := []protocol.CandidateFinding{
		finding("a", protocol.SeverityP1), finding("b", protocol.SeverityP2), finding("c", protocol.SeverityP3),
	}
	verdicts := protocol.EvidenceVerdictEnvelope{Version: 1, RunID: "run"}
	for _, candidate := range findings {
		severity := candidate.Severity
		verdicts.Verdicts = append(verdicts.Verdicts, protocol.EvidenceVerdict{CandidateID: candidate.ID, Verdict: protocol.VerdictAccepted, FinalSeverity: &severity, ReasonCode: "evidence-complete", Reason: "supported"})
	}
	review, err := Aggregate(AggregateInput{
		RunID: "run", GeneratedAt: time.Unix(1, 0), Scope: Scope{Source: snapshot.SourceWorktree, HeadSHA: "head", BaseSHA: "base"},
		Routing: routing.Result{}, MaxParallel: 4, Roles: []RoleExecution{
			{Role: "code-reviewer", Status: "complete"},
			{Role: "qa-expert", Status: "failed", Error: "timeout"},
		},
		Candidates: evidence.CandidateSet{Version: 1, RunID: "run", Findings: findings}, Verdicts: verdicts,
		EvidenceStatus: "validated",
	})
	require.NoError(t, err)
	markdown, err := RenderMarkdown(review)
	require.NoError(t, err)
	assert.Contains(t, string(markdown), "[P1]")
	assert.Contains(t, string(markdown), "[P2]")
	assert.Contains(t, string(markdown), "[P3]")
	assert.Contains(t, string(markdown), "`code-reviewer` — complete")
	assert.Contains(t, string(markdown), "`qa-expert` — failed: timeout")
}

func finding(id string, severity protocol.Severity) protocol.CandidateFinding {
	line := int(id[0]-'a') + 1
	return protocol.CandidateFinding{
		ID: id, Role: "code-reviewer", Severity: severity, Category: "correctness", Title: "finding " + id,
		Location: protocol.FindingLocation{File: "main.go", LineStart: line},
		Evidence: protocol.FindingEvidence{ExecutionPath: "path " + id, ViolatedInvariant: "invariant " + id, FalsifierChecked: "checked"},
		Impact:   "impact " + id, Recommendation: "fix", Confidence: 0.8,
	}
}
