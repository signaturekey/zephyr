package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCandidateBytesCodeFinding(t *testing.T) {
	envelope, err := ValidateCandidateBytes([]byte(validCodeCandidates))
	require.NoError(t, err, "validate candidate bytes")
	assert.Equal(t, ProtocolVersion, envelope.Version)
	assert.Equal(t, "run-1", envelope.RunID)
	assert.Equal(t, "golang-expert", envelope.Role)
	require.Len(t, envelope.Findings, 1)
	assert.Equal(t, SeverityP1, envelope.Findings[0].Severity)
	if !envelope.Findings[0].Location.IsCode() || envelope.Findings[0].Location.IsArtifact() {
		t.Fatalf("unexpected location kind: %+v", envelope.Findings[0].Location)
	}
}

func TestValidateCandidateBytesPlanFinding(t *testing.T) {
	data := strings.ReplaceAll(validCodeCandidates,
		`"location":{"file":"internal/service/handler.go","line_start":42,"line_end":42,"symbol":"Handler.Process"}`,
		`"location":{"artifact":"REVIEW_SPEC.md","section":"Data migration","line_start":81,"line_end":93}`,
	)
	data = strings.Replace(data, `"code":"ctx = context.Background()"`, `"code":null`, 1)

	envelope, err := ValidateCandidateBytes([]byte(data))
	if err != nil {
		t.Fatalf("ValidateCandidateBytes: %v", err)
	}
	if !envelope.Findings[0].Location.IsArtifact() || envelope.Findings[0].Evidence.Code != nil {
		t.Fatalf("unexpected plan finding: %+v", envelope.Findings[0])
	}
}

func TestValidateCandidateBytesAllowsCleanReviewerResult(t *testing.T) {
	envelope, err := ValidateCandidateBytes([]byte(`{"version":1,"run_id":"run-clean","role":"code-reviewer","findings":[]}`))
	if err != nil {
		t.Fatalf("ValidateCandidateBytes: %v", err)
	}
	if len(envelope.Findings) != 0 {
		t.Fatalf("findings = %v, want empty", envelope.Findings)
	}
}

func TestValidateCandidateBytesAcceptsCodexNullableLocationFields(t *testing.T) {
	data := strings.Replace(
		validCodeCandidates,
		`"line_end":42,"symbol":"Handler.Process"`,
		`"line_end":null,"symbol":null`,
		1,
	)
	envelope, err := ValidateCandidateBytes([]byte(data))
	if err != nil {
		t.Fatalf("ValidateCandidateBytes: %v", err)
	}
	location := envelope.Findings[0].Location
	if location.LineEnd != 0 || location.Symbol != "" {
		t.Fatalf("nullable location = %+v, want zero-value optional fields", location)
	}
}

func TestValidateCandidateBytesRejectsSchemaAndSemanticFailures(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unknown property", data: strings.Replace(validCodeCandidates, `"needs_human":false`, `"needs_human":false,"thoughts":"secret"`, 1), want: "additional properties"},
		{name: "severity", data: strings.Replace(validCodeCandidates, `"severity":"P1"`, `"severity":"critical"`, 1), want: "value must be one of"},
		{name: "empty impact", data: strings.Replace(validCodeCandidates, `"impact":"request continues"`, `"impact":""`, 1), want: "minLength"},
		{name: "both location kinds", data: strings.Replace(validCodeCandidates, `"file":"internal/service/handler.go"`, `"file":"internal/service/handler.go","artifact":"REVIEW_SPEC.md","section":"x"`, 1), want: "oneOf"},
		{name: "missing evidence key", data: strings.Replace(validCodeCandidates, ",\n    \"falsifier_checked\":\"checked ownership\"", "", 1), want: "missing property"},
		{name: "mismatched role", data: strings.Replace(validCodeCandidates, `"role":"golang-expert"`, `"role":"sql-expert"`, 1), want: "differs from envelope"},
		{name: "line range", data: strings.Replace(validCodeCandidates, `"line_start":42,"line_end":42`, `"line_start":43,"line_end":42`, 1), want: "line_end before line_start"},
		{name: "trailing JSON", data: validCodeCandidates + `{}`, want: "after top-level value"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ValidateCandidateBytes([]byte(test.data))
			assertInvalidDocument(t, err, test.want)
		})
	}
}

func TestValidateCandidateBytesRejectsDuplicateIDs(t *testing.T) {
	second := candidateFinding
	data := `{"version":1,"run_id":"run-1","role":"golang-expert","findings":[` + candidateFinding + `,` + second + `]}`
	_, err := ValidateCandidateBytes([]byte(data))
	assertInvalidDocument(t, err, "duplicate finding ID")
}

func TestValidateCandidateBytesEnforcesOutputBounds(t *testing.T) {
	oversizedTitle := strings.Repeat("x", 513)
	data := strings.Replace(validCodeCandidates, "request cancellation is lost", oversizedTitle, 1)
	_, err := ValidateCandidateBytes([]byte(data))
	assertInvalidDocument(t, err, "maxLength")

	findings := strings.Repeat(candidateFinding+",", 50) + candidateFinding
	data = `{"version":1,"run_id":"run-1","role":"golang-expert","findings":[` + findings + `]}`
	_, err = ValidateCandidateBytes([]byte(data))
	assertInvalidDocument(t, err, "maxItems")
}

func TestValidateVerdictBytesAllVerdictShapes(t *testing.T) {
	data := `{
  "version": 1,
  "run_id": "run-1",
  "verdicts": [
    {"candidate_id":"c1","verdict":"accepted","final_severity":"P1","reason_code":"evidence-complete","reason":"complete","duplicate_of":null},
    {"candidate_id":"c2","verdict":"downgraded","final_severity":"P2","reason_code":"severity-adjusted","reason":"impact is bounded","duplicate_of":null},
    {"candidate_id":"c3","verdict":"rejected","final_severity":null,"reason_code":"unreachable","reason":"path is not reachable","duplicate_of":null},
    {"candidate_id":"c4","verdict":"duplicate","final_severity":null,"reason_code":"same-root-cause","reason":"same path and impact","duplicate_of":"c1"},
    {"candidate_id":"c5","verdict":"needs-human","final_severity":null,"reason_code":"requirements-unavailable","reason":"business rule unavailable","duplicate_of":null}
  ]
}`

	envelope, err := ValidateVerdictBytes([]byte(data))
	if err != nil {
		t.Fatalf("ValidateVerdictBytes: %v", err)
	}
	if len(envelope.Verdicts) != 5 || envelope.Verdicts[0].FinalSeverity == nil || *envelope.Verdicts[0].FinalSeverity != SeverityP1 {
		t.Fatalf("unexpected verdicts: %+v", envelope.Verdicts)
	}
}

func TestValidateVerdictBytesRejectsInvalidShapesAndSemantics(t *testing.T) {
	tests := []struct {
		name    string
		verdict string
		want    string
	}{
		{name: "accepted without severity", verdict: `{"candidate_id":"c1","verdict":"accepted","final_severity":null,"reason_code":"x","reason":"x","duplicate_of":null}`, want: "final_severity"},
		{name: "rejected with severity", verdict: `{"candidate_id":"c1","verdict":"rejected","final_severity":"P2","reason_code":"x","reason":"x","duplicate_of":null}`, want: "final_severity"},
		{name: "duplicate without target", verdict: `{"candidate_id":"c1","verdict":"duplicate","final_severity":null,"reason_code":"x","reason":"x","duplicate_of":null}`, want: "duplicate_of"},
		{name: "accepted with target", verdict: `{"candidate_id":"c1","verdict":"accepted","final_severity":"P1","reason_code":"x","reason":"x","duplicate_of":"c2"}`, want: "duplicate_of"},
		{name: "self duplicate", verdict: `{"candidate_id":"c1","verdict":"duplicate","final_severity":null,"reason_code":"x","reason":"x","duplicate_of":"c1"}`, want: "duplicate of itself"},
		{name: "unknown verdict", verdict: `{"candidate_id":"c1","verdict":"maybe","final_severity":null,"reason_code":"x","reason":"x","duplicate_of":null}`, want: "value must be one of"},
		{name: "unknown property", verdict: `{"candidate_id":"c1","verdict":"rejected","final_severity":null,"reason_code":"x","reason":"x","duplicate_of":null,"new_finding":{}}`, want: "additional properties"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := `{"version":1,"run_id":"run-1","verdicts":[` + test.verdict + `]}`
			_, err := ValidateVerdictBytes([]byte(data))
			assertInvalidDocument(t, err, test.want)
		})
	}
}

func TestValidateVerdictBytesRejectsDuplicateCandidateVerdicts(t *testing.T) {
	verdict := `{"candidate_id":"c1","verdict":"rejected","final_severity":null,"reason_code":"x","reason":"x","duplicate_of":null}`
	data := `{"version":1,"run_id":"run-1","verdicts":[` + verdict + `,` + verdict + `]}`
	_, err := ValidateVerdictBytes([]byte(data))
	assertInvalidDocument(t, err, "duplicate verdict")
}

func TestValidateVerdictBytesEnforcesOutputBounds(t *testing.T) {
	oversizedReason := strings.Repeat("x", 8193)
	verdict := `{"candidate_id":"c1","verdict":"rejected","final_severity":null,"reason_code":"evidence-incomplete","reason":"` + oversizedReason + `","duplicate_of":null}`
	data := `{"version":1,"run_id":"run-1","verdicts":[` + verdict + `]}`
	_, err := ValidateVerdictBytes([]byte(data))
	assertInvalidDocument(t, err, "maxLength")

	shortVerdict := `{"candidate_id":"c1","verdict":"rejected","final_severity":null,"reason_code":"evidence-incomplete","reason":"unsupported","duplicate_of":null}`
	verdicts := strings.Repeat(shortVerdict+",", 250) + shortVerdict
	data = `{"version":1,"run_id":"run-1","verdicts":[` + verdicts + `]}`
	_, err = ValidateVerdictBytes([]byte(data))
	assertInvalidDocument(t, err, "maxItems")
}

func TestValidateVerdictCandidateIDs(t *testing.T) {
	canonical := "c1"
	valid := EvidenceVerdictEnvelope{Verdicts: []EvidenceVerdict{
		{CandidateID: "c1", Verdict: VerdictAccepted},
		{CandidateID: "c2", Verdict: VerdictDuplicate, DuplicateOf: &canonical},
	}}
	if err := ValidateVerdictCandidateIDs(valid, []string{"c1", "c2"}); err != nil {
		t.Fatalf("ValidateVerdictCandidateIDs: %v", err)
	}

	tests := []struct {
		name       string
		envelope   EvidenceVerdictEnvelope
		candidates []string
		want       string
	}{
		{name: "unknown verdict", envelope: EvidenceVerdictEnvelope{Verdicts: []EvidenceVerdict{{CandidateID: "new"}}}, candidates: []string{"c1"}, want: "unknown candidate"},
		{name: "omitted verdict", envelope: EvidenceVerdictEnvelope{Verdicts: []EvidenceVerdict{{CandidateID: "c1"}}}, candidates: []string{"c1", "c2"}, want: "omitted candidate"},
		{name: "unknown duplicate target", envelope: EvidenceVerdictEnvelope{Verdicts: []EvidenceVerdict{{CandidateID: "c1", DuplicateOf: stringPointer("new")}}}, candidates: []string{"c1"}, want: "unknown canonical"},
		{name: "duplicate expected ID", envelope: EvidenceVerdictEnvelope{}, candidates: []string{"c1", "c1"}, want: "contains duplicate"},
		{name: "empty expected ID", envelope: EvidenceVerdictEnvelope{}, candidates: []string{""}, want: "empty ID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateVerdictCandidateIDs(test.envelope, test.candidates)
			assertInvalidDocument(t, err, test.want)
		})
	}
}

func TestValidateReviewInputBytes(t *testing.T) {
	valid := []byte(`{
  "version": 1,
  "run_id": "run-1",
  "mode": "plan",
  "source": "plan-only",
  "repository": {"root":""},
  "changed_files": [],
  "technologies": ["go"],
  "diff": {"truncated":false,"total_bytes":0},
  "plan": {"kind":"plan","path":"REVIEW_SPEC.md","content_hash":"sha256:abc","content":"# Plan","truncated":false},
  "business_context": [
    {"source":"jira","key":"RINT-1","url":"https://jira.example/RINT-1","fetched_at":"2026-08-02T12:00:00Z","content_hash":"sha256:def","content":"requirements"}
  ],
  "project_instructions": [],
  "sources": {"included":["REVIEW_SPEC.md"],"excluded":[],"unavailable":[]},
  "routing_signals": ["architecture"],
  "strong_routing_signals": [],
  "coverage_limits": [],
  "restrictions": ["read-only review"]
}`)
	if err := ValidateReviewInputBytes(valid); err != nil {
		t.Fatalf("ValidateReviewInputBytes: %v", err)
	}

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "missing run", data: strings.Replace(string(valid), `"run_id": "run-1",`, "", 1), want: "missing property"},
		{name: "bad timestamp", data: strings.Replace(string(valid), "2026-08-02T12:00:00Z", "yesterday", 1), want: "date-time"},
		{name: "unknown field", data: strings.Replace(string(valid), `"version": 1,`, `"version": 1,"thoughts":[],`, 1), want: "additional properties"},
		{name: "duplicate changed file", data: strings.Replace(string(valid), `"changed_files": []`, `"changed_files": ["a.go","a.go"]`, 1), want: "items at 0 and 1 are equal"},
		{name: "unsupported source", data: strings.Replace(string(valid), `"source": "plan-only"`, `"source": "github"`, 1), want: "value must be one of"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateReviewInputBytes([]byte(test.data))
			assertInvalidDocument(t, err, test.want)
		})
	}
}

func TestValidateSemanticRoutingBytes(t *testing.T) {
	input := []byte(`{"version":1,"run_id":"run-1","decisions":[{"role":"sql-expert","decision":"exclude","evidence_refs":["scope"],"reason":"outside scope","confidence":0.9}]}`)
	envelope, err := ValidateSemanticRoutingBytes(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(envelope.Decisions) != 1 || envelope.Decisions[0].Role != "sql-expert" {
		t.Fatalf("unexpected semantic routing envelope: %#v", envelope)
	}
	repeatedEvidence := []byte(`{"version":1,"run_id":"run-1","decisions":[{"role":"sql-expert","decision":"exclude","evidence_refs":["scope","scope"],"reason":"outside scope","confidence":0.9}]}`)
	if _, err := ValidateSemanticRoutingBytes(repeatedEvidence); err != nil {
		t.Fatalf("producer-compatible repeated evidence refs should be normalized by routing core: %v", err)
	}
	duplicate := []byte(`{"version":1,"run_id":"run-1","decisions":[{"role":"sql-expert","decision":"exclude","evidence_refs":["scope"],"reason":"outside scope","confidence":0.9},{"role":"sql-expert","decision":"include","evidence_refs":["scope"],"reason":"duplicate","confidence":0.9}]}`)
	if _, err := ValidateSemanticRoutingBytes(duplicate); err == nil {
		t.Fatal("duplicate semantic role unexpectedly validated")
	}
}

func assertInvalidDocument(t *testing.T, err error, contains string) {
	t.Helper()
	require.Error(t, err, "validation unexpectedly succeeded")
	assert.ErrorIs(t, err, ErrInvalidDocument)
	assert.Contains(t, err.Error(), contains)
}

func stringPointer(value string) *string { return &value }

const validCodeCandidates = `{
  "version": 1,
  "run_id": "run-1",
  "role": "golang-expert",
  "findings": [` + candidateFinding + `]
}`

const candidateFinding = `{
  "id":"golang-expert-001",
  "role":"golang-expert",
  "severity":"P1",
  "category":"context-propagation",
  "title":"request cancellation is lost",
  "location":{"file":"internal/service/handler.go","line_start":42,"line_end":42,"symbol":"Handler.Process"},
  "evidence":{
    "code":"ctx = context.Background()",
    "execution_path":"RPC -> service -> downstream",
    "violated_invariant":"downstream I/O inherits cancellation",
    "requirement_source":null,
    "falsifier_checked":"checked ownership"
  },
  "impact":"request continues",
  "recommendation":"pass request context",
  "confidence":0.93,
  "needs_human":false
}`
