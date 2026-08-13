package workflow

import (
	"strings"
	"testing"

	"github.com/signaturekey/zephyr/internal/redaction"
	"github.com/signaturekey/zephyr/internal/schema"
)

func TestSanitizeAgentOutputs(t *testing.T) {
	code := `password="hunter2"`
	requirement := `{"token":"live-token"}`
	candidates := sanitizeCandidateEnvelope(schema.CandidateEnvelope{
		Findings: []schema.CandidateFinding{{
			Title: "password=hunter2",
			Evidence: schema.FindingEvidence{
				Code: &code, RequirementSource: &requirement,
			},
			Impact: "access_token=live-token",
		}},
	}, redaction.DefaultPolicy(nil))
	verdicts := sanitizeVerdicts(schema.EvidenceVerdictEnvelope{
		Verdicts: []schema.EvidenceVerdict{{Reason: "client_secret=top-secret"}},
	}, redaction.DefaultPolicy(nil))
	combined := candidates.Findings[0].Title + *candidates.Findings[0].Evidence.Code +
		*candidates.Findings[0].Evidence.RequirementSource + candidates.Findings[0].Impact + verdicts.Verdicts[0].Reason
	for _, test := range []struct {
		name, secret string
	}{
		{name: "password", secret: "hunter2"},
		{name: "token", secret: "live-token"},
		{name: "client secret", secret: "top-secret"},
	} {
		if strings.Contains(combined, test.secret) {
			t.Errorf("agent output leaked %s", test.name)
		}
	}
}
