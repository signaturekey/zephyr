package evidence

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/signaturekey/zephyr/internal/schema"
)

func ValidateVerdicts(envelope schema.EvidenceVerdictEnvelope, candidates CandidateSet) error {
	var problems []string
	if envelope.Version != schema.ProtocolVersion || candidates.Version != schema.ProtocolVersion {
		problems = append(problems, "protocol version mismatch")
	}
	if envelope.RunID != candidates.RunID {
		problems = append(problems, "verdict run_id does not match candidate set")
	}

	byID := make(map[string]schema.CandidateFinding, len(candidates.Findings))
	for _, finding := range candidates.Findings {
		if _, duplicate := byID[finding.ID]; duplicate {
			problems = append(problems, fmt.Sprintf("candidate set contains duplicate ID %q", finding.ID))
		}
		byID[finding.ID] = finding
	}
	verdictByID := make(map[string]schema.EvidenceVerdict, len(envelope.Verdicts))
	for _, verdict := range envelope.Verdicts {
		candidate, exists := byID[verdict.CandidateID]
		if !exists {
			problems = append(problems, fmt.Sprintf("verdict references unknown candidate %q", verdict.CandidateID))
			continue
		}
		if _, duplicate := verdictByID[verdict.CandidateID]; duplicate {
			problems = append(problems, fmt.Sprintf("candidate %q has multiple verdicts", verdict.CandidateID))
			continue
		}
		verdictByID[verdict.CandidateID] = verdict
		problems = append(problems, validateOneVerdict(verdict, candidate, byID)...)
	}
	for id := range byID {
		if _, exists := verdictByID[id]; !exists {
			problems = append(problems, fmt.Sprintf("candidate %q is missing a verdict", id))
		}
	}

	for _, verdict := range envelope.Verdicts {
		if verdict.Verdict != schema.VerdictDuplicate || verdict.DuplicateOf == nil {
			continue
		}
		target, exists := verdictByID[*verdict.DuplicateOf]
		if exists && target.Verdict != schema.VerdictAccepted && target.Verdict != schema.VerdictDowngraded {
			problems = append(problems, fmt.Sprintf("duplicate %q points to non-final candidate %q", verdict.CandidateID, *verdict.DuplicateOf))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "; "))
}

func validateOneVerdict(verdict schema.EvidenceVerdict, candidate schema.CandidateFinding, candidates map[string]schema.CandidateFinding) []string {
	var problems []string
	if !verdict.Verdict.Valid() {
		return []string{fmt.Sprintf("candidate %q has invalid verdict %q", verdict.CandidateID, verdict.Verdict)}
	}
	if strings.TrimSpace(verdict.ReasonCode) == "" || strings.TrimSpace(verdict.Reason) == "" {
		problems = append(problems, fmt.Sprintf("candidate %q verdict lacks reason code or reason", verdict.CandidateID))
	}
	switch verdict.Verdict {
	case schema.VerdictAccepted:
		if verdict.FinalSeverity == nil || *verdict.FinalSeverity != candidate.Severity {
			problems = append(problems, fmt.Sprintf("accepted candidate %q must retain severity %s", verdict.CandidateID, candidate.Severity))
		}
		if verdict.DuplicateOf != nil {
			problems = append(problems, fmt.Sprintf("accepted candidate %q cannot set duplicate_of", verdict.CandidateID))
		}
	case schema.VerdictDowngraded:
		if verdict.FinalSeverity == nil || !verdict.FinalSeverity.Valid() || verdict.FinalSeverity.Rank() <= candidate.Severity.Rank() {
			problems = append(problems, fmt.Sprintf("downgraded candidate %q must have a strictly lower final severity", verdict.CandidateID))
		}
		if verdict.DuplicateOf != nil {
			problems = append(problems, fmt.Sprintf("downgraded candidate %q cannot set duplicate_of", verdict.CandidateID))
		}
	case schema.VerdictRejected:
		if verdict.FinalSeverity != nil || verdict.DuplicateOf != nil {
			problems = append(problems, fmt.Sprintf("rejected candidate %q cannot set final_severity or duplicate_of", verdict.CandidateID))
		}
	case schema.VerdictDuplicate:
		if verdict.FinalSeverity != nil || verdict.DuplicateOf == nil {
			problems = append(problems, fmt.Sprintf("duplicate candidate %q requires only duplicate_of", verdict.CandidateID))
		} else {
			if *verdict.DuplicateOf == verdict.CandidateID {
				problems = append(problems, fmt.Sprintf("candidate %q cannot duplicate itself", verdict.CandidateID))
			}
			if _, exists := candidates[*verdict.DuplicateOf]; !exists {
				problems = append(problems, fmt.Sprintf("duplicate candidate %q points to unknown candidate %q", verdict.CandidateID, *verdict.DuplicateOf))
			}
		}
	case schema.VerdictNeedsHuman:
		if verdict.DuplicateOf != nil {
			problems = append(problems, fmt.Sprintf("needs-human candidate %q cannot set duplicate_of", verdict.CandidateID))
		}
		if verdict.FinalSeverity != nil && (!verdict.FinalSeverity.Valid() || verdict.FinalSeverity.Rank() < candidate.Severity.Rank()) {
			problems = append(problems, fmt.Sprintf("needs-human candidate %q cannot increase severity", verdict.CandidateID))
		}
	}
	return problems
}
