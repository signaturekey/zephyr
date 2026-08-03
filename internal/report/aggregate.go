package report

import (
	"fmt"
	"sort"

	"github.com/signaturekey/zephyr/internal/dedupe"
	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/schema"
)

func Aggregate(input AggregateInput) (Review, RejectedArtifact, error) {
	if input.RunID == "" || input.Candidates.RunID != input.RunID || input.Verdicts.RunID != input.RunID {
		return Review{}, RejectedArtifact{}, fmt.Errorf("aggregate inputs must belong to one non-empty run")
	}
	if err := validateVerdicts(input); err != nil {
		return Review{}, RejectedArtifact{}, err
	}
	if input.GeneratedAt.IsZero() {
		return Review{}, RejectedArtifact{}, fmt.Errorf("aggregate generated_at is required")
	}

	candidates := make(map[string]schema.CandidateFinding, len(input.Candidates.Findings))
	for _, finding := range input.Candidates.Findings {
		candidates[finding.ID] = finding
	}
	verdicts := make(map[string]schema.EvidenceVerdict, len(input.Verdicts.Verdicts))
	accepted := make([]schema.CandidateFinding, 0, len(input.Candidates.Findings))
	questions := make([]HumanQuestion, 0)
	rejected := make([]RejectedCandidate, 0)
	for _, verdict := range input.Verdicts.Verdicts {
		verdicts[verdict.CandidateID] = verdict
		finding := candidates[verdict.CandidateID]
		switch verdict.Verdict {
		case schema.VerdictAccepted, schema.VerdictDowngraded:
			finding.Severity = *verdict.FinalSeverity
			accepted = append(accepted, finding)
		case schema.VerdictNeedsHuman:
			if verdict.FinalSeverity != nil {
				finding.Severity = *verdict.FinalSeverity
			}
			questions = append(questions, HumanQuestion{Candidate: finding, Reason: verdict.Reason})
		case schema.VerdictRejected:
			rejected = append(rejected, RejectedCandidate{
				CandidateID: finding.ID,
				Role:        finding.Role,
				Stage:       "evidence-gate",
				ReasonCode:  verdict.ReasonCode,
				Reason:      verdict.Reason,
			})
		}
	}
	for _, precheck := range input.PrecheckReports {
		for _, item := range precheck.Rejected {
			rejected = append(rejected, RejectedCandidate{
				CandidateID: item.CandidateID,
				Role:        item.Role,
				Stage:       "deterministic-precheck",
				ReasonCode:  item.ReasonCode,
				Reason:      item.Reason,
			})
		}
	}

	groups := dedupe.GroupFindings(accepted)
	final := make([]FinalFinding, 0, len(groups))
	byID := make(map[string]int)
	for _, group := range groups {
		gateReason := ""
		if verdict, ok := verdicts[group.Canonical.ID]; ok {
			gateReason = verdict.Reason
		}
		item := FinalFinding{
			Candidate:    group.Canonical,
			SourceRoles:  append([]string(nil), group.SourceRoles...),
			DuplicateIDs: append([]string(nil), group.DuplicateIDs...),
			GateReason:   gateReason,
		}
		index := len(final)
		final = append(final, item)
		byID[group.Canonical.ID] = index
		for _, member := range group.Members {
			byID[member.ID] = index
		}
	}
	for _, verdict := range input.Verdicts.Verdicts {
		if verdict.Verdict != schema.VerdictDuplicate || verdict.DuplicateOf == nil {
			continue
		}
		index, ok := byID[*verdict.DuplicateOf]
		if !ok {
			return Review{}, RejectedArtifact{}, fmt.Errorf("duplicate %q points to a non-final finding %q", verdict.CandidateID, *verdict.DuplicateOf)
		}
		duplicate := candidates[verdict.CandidateID]
		final[index].SourceRoles = appendUniqueSorted(final[index].SourceRoles, duplicate.Role)
		final[index].DuplicateIDs = appendUniqueSorted(final[index].DuplicateIDs, duplicate.ID)
	}

	sortFinal(final)
	sort.Slice(questions, func(i, j int) bool { return betterCandidate(questions[i].Candidate, questions[j].Candidate) })
	sort.Slice(rejected, func(i, j int) bool {
		if rejected[i].CandidateID == rejected[j].CandidateID {
			return rejected[i].Stage < rejected[j].Stage
		}
		return rejected[i].CandidateID < rejected[j].CandidateID
	})

	byReason := make(map[string]int)
	for _, item := range rejected {
		byReason[item.ReasonCode]++
	}
	coverage := uniqueSorted(input.CoverageLimits)
	status := "complete"
	if len(coverage) > 0 || input.Scope.Stale {
		status = "complete-with-limits"
	}
	review := Review{
		Version:        Version,
		RunID:          input.RunID,
		GeneratedAt:    input.GeneratedAt.UTC(),
		Status:         status,
		Scope:          input.Scope,
		Routing:        input.Routing,
		Findings:       final,
		NeedsHuman:     questions,
		CoverageLimits: coverage,
		Rejected: RejectedSummary{
			Count:    len(rejected),
			ByReason: byReason,
			Path:     input.RejectedPath,
		},
	}
	return review, RejectedArtifact{Version: Version, RunID: input.RunID, Rejected: rejected}, nil
}

func validateVerdicts(input AggregateInput) error {
	return evidence.ValidateVerdicts(input.Verdicts, input.Candidates)
}

func sortFinal(findings []FinalFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		return betterCandidate(findings[i].Candidate, findings[j].Candidate)
	})
}

func betterCandidate(left, right schema.CandidateFinding) bool {
	if left.Severity.Rank() != right.Severity.Rank() {
		return left.Severity.Rank() < right.Severity.Rank()
	}
	if left.Confidence != right.Confidence {
		return left.Confidence > right.Confidence
	}
	return left.ID < right.ID
}

func appendUniqueSorted(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	sort.Strings(values)
	return values
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
