package report

import (
	"testing"
	"time"

	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/signaturekey/zephyr/internal/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAggregateAndRenderKeepsEverySeverity(t *testing.T) {
	findings := []schema.CandidateFinding{
		finding("a", schema.SeverityP1), finding("b", schema.SeverityP2), finding("c", schema.SeverityP3),
	}
	verdicts := schema.EvidenceVerdictEnvelope{Version: 1, RunID: "run"}
	for _, candidate := range findings {
		severity := candidate.Severity
		verdicts.Verdicts = append(verdicts.Verdicts, schema.EvidenceVerdict{CandidateID: candidate.ID, Verdict: schema.VerdictAccepted, FinalSeverity: &severity, ReasonCode: "evidence-complete", Reason: "supported"})
	}
	review, err := Aggregate(AggregateInput{
		RunID: "run", GeneratedAt: time.Unix(1, 0), Scope: Scope{Source: snapshot.SourceWorktree, HeadSHA: "head", BaseSHA: "base"},
		Routing: routing.Result{}, MaxParallel: 4,
		Candidates: evidence.CandidateSet{Version: 1, RunID: "run", Findings: findings}, Verdicts: verdicts,
		EvidenceStatus: "validated",
	})
	require.NoError(t, err)
	markdown, err := RenderMarkdown(review)
	require.NoError(t, err)
	assert.Contains(t, string(markdown), "[P1]")
	assert.Contains(t, string(markdown), "[P2]")
	assert.Contains(t, string(markdown), "[P3]")
}

func finding(id string, severity schema.Severity) schema.CandidateFinding {
	line := int(id[0]-'a') + 1
	return schema.CandidateFinding{
		ID: id, Role: "code-reviewer", Severity: severity, Category: "correctness", Title: "finding " + id,
		Location: schema.FindingLocation{File: "main.go", LineStart: line},
		Evidence: schema.FindingEvidence{ExecutionPath: "path " + id, ViolatedInvariant: "invariant " + id, FalsifierChecked: "checked"},
		Impact:   "impact " + id, Recommendation: "fix", Confidence: 0.8,
	}
}
