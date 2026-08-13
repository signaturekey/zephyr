package report

import (
	"strings"
	"testing"
	"time"

	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAggregateAppliesVerdictsAndExplicitDuplicates(t *testing.T) {
	canonical := reportCandidate("code-reviewer-001", "code-reviewer", schema.SeverityP1)
	duplicate := reportCandidate("golang-expert-001", "golang-expert", schema.SeverityP1)
	finalSeverity := schema.SeverityP1
	canonicalID := canonical.ID
	input := AggregateInput{
		RunID:       "run",
		GeneratedAt: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
		Scope:       Scope{Mode: "implementation", Source: "working-tree"},
		Candidates:  evidence.CandidateSet{Version: 1, RunID: "run", Findings: []schema.CandidateFinding{canonical, duplicate}},
		Verdicts: schema.EvidenceVerdictEnvelope{Version: 1, RunID: "run", Verdicts: []schema.EvidenceVerdict{
			{CandidateID: canonical.ID, Verdict: schema.VerdictAccepted, FinalSeverity: &finalSeverity, ReasonCode: "evidence-complete", Reason: "complete evidence"},
			{CandidateID: duplicate.ID, Verdict: schema.VerdictDuplicate, ReasonCode: "same-root-cause", Reason: "same defect", DuplicateOf: &canonicalID},
		}},
		RejectedPath: "rejected-findings.json",
	}
	review, rejected, err := Aggregate(input)
	require.NoError(t, err)
	if len(review.Findings) != 1 || len(review.Findings[0].SourceRoles) != 2 || len(review.Findings[0].DuplicateIDs) != 1 {
		t.Fatalf("unexpected findings: %#v", review.Findings)
	}
	assert.Empty(t, rejected.Rejected)
}

func TestAggregateKeepsPrecheckAndGateRejections(t *testing.T) {
	finding := reportCandidate("qa-expert-001", "qa-expert", schema.SeverityP2)
	input := AggregateInput{
		RunID:       "run",
		GeneratedAt: time.Now().UTC(),
		Candidates:  evidence.CandidateSet{Version: 1, RunID: "run", Findings: []schema.CandidateFinding{finding}},
		Verdicts: schema.EvidenceVerdictEnvelope{
			Version: 1,
			RunID:   "run",
			Verdicts: []schema.EvidenceVerdict{
				{CandidateID: finding.ID, Verdict: schema.VerdictRejected, ReasonCode: "evidence-incomplete", Reason: "not demonstrated"},
			},
		},
		PrecheckReports: []evidence.PrecheckReport{
			{
				Version: 1,
				RunID:   "run",
				Role:    "golang-expert",
				Rejected: []evidence.Rejection{
					{CandidateID: "golang-expert-002", Role: "golang-expert", ReasonCode: "invalid-line", Reason: "outside file"},
				},
			},
		},
		RejectedPath: "rejected-findings.json",
	}
	review, artifact, err := Aggregate(input)
	if err != nil {
		t.Fatal(err)
	}
	if review.Rejected.Count != 2 || len(artifact.Rejected) != 2 || len(review.Findings) != 0 {
		t.Fatalf("unexpected rejection aggregation: review=%#v artifact=%#v", review, artifact)
	}
}

func TestRenderMarkdownLimitsNoiseAndShowsHonestEmptyResult(t *testing.T) {
	empty := Review{
		Version:    1,
		RunID:      "run",
		Status:     "complete",
		Scope:      Scope{Mode: "plan", Source: "plan-only", Repository: "/repo"},
		Routing:    RoutingSummary{},
		Findings:   []FinalFinding{},
		NeedsHuman: []HumanQuestion{},
		Rejected:   RejectedSummary{ByReason: map[string]int{}, Path: "rejected-findings.json"},
	}
	data, err := RenderMarkdown(empty, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Доказуемых проблем в проверенной области не найдено") || strings.Contains(string(data), "полностью корректен") {
		t.Fatalf("unexpected empty report:\n%s", data)
	}
}

func TestRenderMarkdownHidesP3WhenHigherSeverityExists(t *testing.T) {
	p1 := FinalFinding{Candidate: reportCandidate("code-reviewer-001", "code-reviewer", schema.SeverityP1), SourceRoles: []string{"code-reviewer"}}
	p3 := FinalFinding{Candidate: reportCandidate("code-simplifier-001", "code-simplifier", schema.SeverityP3), SourceRoles: []string{"code-simplifier"}}
	p3.Candidate.Title = "P3 unique"
	review := Review{
		Version: 1, RunID: "run", Status: "complete", Scope: Scope{Mode: "implementation", Source: "working-tree"},
		Findings: []FinalFinding{p1, p3}, Rejected: RejectedSummary{ByReason: map[string]int{}},
	}
	data, err := RenderMarkdown(review, RenderOptions{MaxFinalFindings: 30})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), p3.Candidate.Title) {
		t.Fatal("P3 unexpectedly shown")
	}
	if !strings.Contains(string(data), "P3=1") {
		t.Fatalf("omitted P3 count missing:\n%s", data)
	}
}

func TestRenderMarkdownEscapesUntrustedMarkdownAndHTML(t *testing.T) {
	finding := FinalFinding{Candidate: reportCandidate("code-reviewer-001", "code-reviewer", schema.SeverityP1), SourceRoles: []string{"code-reviewer"}}
	finding.Candidate.Title = `![remote](https://tracker.invalid/pixel) <img src=x> # forged`
	finding.Candidate.Location.File = "odd`name\n# forged.go"
	finding.Candidate.Impact = `<script>alert(1)</script>`
	review := Review{
		Version: 1, RunID: "run", Status: "complete",
		Scope:    Scope{Mode: "implementation", Source: "working-tree", Repository: `![repo](https://tracker.invalid/repo)`},
		Findings: []FinalFinding{finding}, Rejected: RejectedSummary{ByReason: map[string]int{}, Path: "rejected-findings.json"},
	}
	data, err := RenderMarkdown(review, RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	for _, active := range []string{"![remote]", "![repo]", " <img", " <script", "\n# forged"} {
		if strings.Contains(output, active) {
			t.Fatalf("active Markdown/HTML %q survived:\n%s", active, output)
		}
	}
}

func reportCandidate(id, role string, severity schema.Severity) schema.CandidateFinding {
	return schema.CandidateFinding{
		ID: id, Role: role, Severity: severity, Category: "correctness", Title: "Concrete defect",
		Location: schema.FindingLocation{File: "handler.go", LineStart: 10},
		Evidence: schema.FindingEvidence{ExecutionPath: "handler -> client", ViolatedInvariant: "request must be safe", FalsifierChecked: "checked guard"},
		Impact:   "observable failure", Recommendation: "preserve the invariant", Confidence: 0.9,
	}
}
