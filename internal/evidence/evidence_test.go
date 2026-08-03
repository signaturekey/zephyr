package evidence

import (
	"testing"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/schema"
)

func TestPrecheckAcceptsSupportedCodeFinding(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	code := "func handler() {}"
	finding := candidate("code-reviewer-001", "code-reviewer", schema.SeverityP1)
	finding.Location = schema.FindingLocation{File: "handler.go", LineStart: 3, LineEnd: 3}
	finding.Evidence.Code = &code

	report := Precheck(schema.CandidateEnvelope{Version: 1, RunID: "run", Role: "code-reviewer", Findings: []schema.CandidateFinding{finding}}, contextpack.Packet{
		Version:      1,
		RunID:        "run",
		Mode:         "implementation",
		Repository:   contextpack.Repository{Root: "/checkout/not-used-by-precheck"},
		ChangedFiles: []string{"handler.go"},
		Diff:         contextpack.Diff{Full: "diff --git a/handler.go b/handler.go\n--- a/handler.go\n+++ b/handler.go\n@@ -1,3 +1,3 @@\n package demo\n \n func handler() {}\n"},
	}, cfg)
	if len(report.Accepted) != 1 || len(report.Rejected) != 0 {
		t.Fatalf("unexpected precheck: %#v", report)
	}
}

func TestPrecheckRejectsLineAbsentFromImmutableDiff(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	code := "return wrong"
	finding := candidate("code-reviewer-001", "code-reviewer", schema.SeverityP1)
	finding.Location = schema.FindingLocation{File: "handler.go", LineStart: 99}
	finding.Evidence.Code = &code
	report := Precheck(schema.CandidateEnvelope{Version: 1, RunID: "run", Role: "code-reviewer", Findings: []schema.CandidateFinding{finding}}, contextpack.Packet{
		Version: 1, RunID: "run", Mode: "implementation", ChangedFiles: []string{"handler.go"},
		Diff: contextpack.Diff{Full: "+++ b/handler.go\n@@ -1 +1 @@\n-old\n+new\n"},
	}, cfg)
	if len(report.Rejected) != 1 || report.Rejected[0].ReasonCode != "line-out-of-snapshot" {
		t.Fatalf("unexpected rejection: %#v", report.Rejected)
	}
}

func TestPrecheckRejectsHighSeverityCodeFragmentAbsentFromImmutableDiff(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	code := "return privilegedData"
	finding := candidate("code-reviewer-001", "code-reviewer", schema.SeverityP1)
	finding.Location = schema.FindingLocation{File: "handler.go", LineStart: 1}
	finding.Evidence.Code = &code
	report := Precheck(schema.CandidateEnvelope{Version: 1, RunID: "run", Role: "code-reviewer", Findings: []schema.CandidateFinding{finding}}, contextpack.Packet{
		Version: 1, RunID: "run", Mode: "implementation", ChangedFiles: []string{"handler.go"},
		Diff: contextpack.Diff{Full: "+++ b/handler.go\n@@ -1 +1 @@\n-return old\n+return safeData\n"},
	}, cfg)
	if len(report.Rejected) != 1 || report.Rejected[0].ReasonCode != "evidence-code-not-in-snapshot" {
		t.Fatalf("unexpected rejection: %#v", report.Rejected)
	}
}

func TestPrecheckRejectsOutOfScopeAndHighSimplifier(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	finding := candidate("code-simplifier-001", "code-simplifier", schema.SeverityP1)
	finding.Category = string(CategoryAvoidableComplexity)
	finding.Location = schema.FindingLocation{File: "other.go", LineStart: 1}
	code := "x"
	finding.Evidence.Code = &code
	report := Precheck(schema.CandidateEnvelope{Version: 1, RunID: "run", Role: "code-simplifier", Findings: []schema.CandidateFinding{finding}}, contextpack.Packet{
		Version: 1, RunID: "run", Mode: "implementation", ChangedFiles: []string{"changed.go"},
	}, cfg)
	if len(report.Rejected) != 1 || report.Rejected[0].ReasonCode != "severity-not-allowed" {
		t.Fatalf("unexpected rejection: %#v", report.Rejected)
	}
}

func TestPrecheckPlanFindingWithoutLine(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	finding := candidate("architect-reviewer-001", "architect-reviewer", schema.SeverityP2)
	finding.Category = string(CategoryPlanCompleteness)
	finding.Location = schema.FindingLocation{Artifact: "REVIEW_SPEC.md", Section: "Missing rollback section"}
	report := Precheck(schema.CandidateEnvelope{Version: 1, RunID: "run", Role: "architect-reviewer", Findings: []schema.CandidateFinding{finding}}, contextpack.Packet{
		Version: 1, RunID: "run", Mode: "plan", Plan: &contextpack.Document{Path: "/repo/REVIEW_SPEC.md", Content: "# Plan\n"},
	}, cfg)
	if len(report.Accepted) != 1 {
		t.Fatalf("unexpected plan precheck: %#v", report)
	}
}

func TestValidateVerdicts(t *testing.T) {
	finding := candidate("code-reviewer-001", "code-reviewer", schema.SeverityP1)
	final := schema.SeverityP1
	envelope := schema.EvidenceVerdictEnvelope{Version: 1, RunID: "run", Verdicts: []schema.EvidenceVerdict{{
		CandidateID: finding.ID, Verdict: schema.VerdictAccepted, FinalSeverity: &final, ReasonCode: "evidence-complete", Reason: "supported",
	}}}
	if err := ValidateVerdicts(envelope, CandidateSet{Version: 1, RunID: "run", Findings: []schema.CandidateFinding{finding}}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateVerdictsRejectsSeverityIncreaseAndMissingID(t *testing.T) {
	finding := candidate("qa-expert-001", "qa-expert", schema.SeverityP2)
	higher := schema.SeverityP1
	envelope := schema.EvidenceVerdictEnvelope{Version: 1, RunID: "run", Verdicts: []schema.EvidenceVerdict{{
		CandidateID: finding.ID, Verdict: schema.VerdictDowngraded, FinalSeverity: &higher, ReasonCode: "wrong", Reason: "wrong",
	}, {
		CandidateID: "unknown-001", Verdict: schema.VerdictRejected, ReasonCode: "unknown", Reason: "unknown",
	}}}
	if err := ValidateVerdicts(envelope, CandidateSet{Version: 1, RunID: "run", Findings: []schema.CandidateFinding{finding}}); err == nil {
		t.Fatal("expected integrity validation error")
	}
}

func candidate(id, role string, severity schema.Severity) schema.CandidateFinding {
	return schema.CandidateFinding{
		ID:       id,
		Role:     role,
		Severity: severity,
		Category: "correctness",
		Title:    "Concrete defect",
		Evidence: schema.FindingEvidence{
			ExecutionPath:     "entry -> failure",
			ViolatedInvariant: "operation must remain correct",
			FalsifierChecked:  "checked the alternate branch",
		},
		Impact:         "observable failure",
		Recommendation: "preserve the invariant",
		Confidence:     0.9,
	}
}
