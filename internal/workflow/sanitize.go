package workflow

import (
	"github.com/signaturekey/zephyr/internal/redaction"
	"github.com/signaturekey/zephyr/internal/schema"
)

func sanitizeCandidateEnvelope(value schema.CandidateEnvelope, policy redaction.Policy) schema.CandidateEnvelope {
	for index := range value.Findings {
		finding := &value.Findings[index]
		finding.ID = policy.Text(finding.ID)
		finding.Category = policy.Text(finding.Category)
		finding.Title = policy.Text(finding.Title)
		finding.Location.File = policy.Text(finding.Location.File)
		finding.Location.Artifact = policy.Text(finding.Location.Artifact)
		finding.Location.Section = policy.Text(finding.Location.Section)
		finding.Location.Symbol = policy.Text(finding.Location.Symbol)
		finding.Evidence.ExecutionPath = policy.Text(finding.Evidence.ExecutionPath)
		finding.Evidence.ViolatedInvariant = policy.Text(finding.Evidence.ViolatedInvariant)
		finding.Evidence.FalsifierChecked = policy.Text(finding.Evidence.FalsifierChecked)
		if finding.Evidence.Code != nil {
			content := policy.Text(*finding.Evidence.Code)
			finding.Evidence.Code = &content
		}
		if finding.Evidence.RequirementSource != nil {
			content := policy.Text(*finding.Evidence.RequirementSource)
			finding.Evidence.RequirementSource = &content
		}
		finding.Impact = policy.Text(finding.Impact)
		finding.Recommendation = policy.Text(finding.Recommendation)
	}
	return value
}

func sanitizeVerdicts(value schema.EvidenceVerdictEnvelope, policy redaction.Policy) schema.EvidenceVerdictEnvelope {
	for index := range value.Verdicts {
		value.Verdicts[index].CandidateID = policy.Text(value.Verdicts[index].CandidateID)
		value.Verdicts[index].ReasonCode = policy.Text(value.Verdicts[index].ReasonCode)
		value.Verdicts[index].Reason = policy.Text(value.Verdicts[index].Reason)
		if value.Verdicts[index].DuplicateOf != nil {
			content := policy.Text(*value.Verdicts[index].DuplicateOf)
			value.Verdicts[index].DuplicateOf = &content
		}
	}
	return value
}

func sanitizeSemanticRouting(value schema.SemanticRoutingEnvelope, policy redaction.Policy) schema.SemanticRoutingEnvelope {
	for index := range value.Decisions {
		value.Decisions[index].Reason = policy.Text(value.Decisions[index].Reason)
		value.Decisions[index].EvidenceRefs = policy.Strings(value.Decisions[index].EvidenceRefs)
	}
	return value
}
