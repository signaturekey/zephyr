package report

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/signaturekey/zephyr/internal/dedupe"
	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/protocol"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/snapshot"
)

const Version = 2

type Scope struct {
	Source       snapshot.Source `json:"source"`
	Repository   string          `json:"repository"`
	Branch       string          `json:"branch,omitempty"`
	HeadSHA      string          `json:"head_sha"`
	BaseRef      string          `json:"base_ref,omitempty"`
	BaseSHA      string          `json:"base_sha"`
	MergeBase    string          `json:"merge_base,omitempty"`
	ChangedFiles []string        `json:"changed_files"`
	Contexts     []string        `json:"contexts"`
}

type RoleExecution struct {
	Role   string `json:"role"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type FinalFinding struct {
	Candidate    protocol.CandidateFinding `json:"candidate"`
	SourceRoles  []string                  `json:"source_roles"`
	DuplicateIDs []string                  `json:"duplicate_ids"`
	GateReason   string                    `json:"gate_reason"`
}

type HumanQuestion struct {
	Candidate protocol.CandidateFinding `json:"candidate"`
	Reason    string                    `json:"reason"`
}

type RejectedCandidate struct {
	CandidateID string `json:"candidate_id"`
	Role        string `json:"role,omitempty"`
	Stage       string `json:"stage"`
	ReasonCode  string `json:"reason_code"`
	Reason      string `json:"reason"`
}

type Review struct {
	Version        int                 `json:"version"`
	RunID          string              `json:"run_id"`
	GeneratedAt    time.Time           `json:"generated_at"`
	Status         string              `json:"status"`
	Scope          Scope               `json:"scope"`
	Routing        routing.Result      `json:"routing"`
	MaxParallel    int                 `json:"max_parallel"`
	Roles          []RoleExecution     `json:"roles"`
	EvidenceStatus string              `json:"evidence_status"`
	Findings       []FinalFinding      `json:"findings"`
	NeedsHuman     []HumanQuestion     `json:"needs_human"`
	CoverageLimits []string            `json:"coverage_limits"`
	Rejected       []RejectedCandidate `json:"rejected_candidates"`
}

type AggregateInput struct {
	RunID           string
	GeneratedAt     time.Time
	Scope           Scope
	Routing         routing.Result
	MaxParallel     int
	Roles           []RoleExecution
	Candidates      evidence.CandidateSet
	Verdicts        protocol.EvidenceVerdictEnvelope
	PrecheckReports []evidence.PrecheckReport
	CoverageLimits  []string
	EvidenceStatus  string
}

func Aggregate(input AggregateInput) (Review, error) {
	if input.RunID == "" || input.Candidates.RunID != input.RunID || input.Verdicts.RunID != input.RunID {
		return Review{}, fmt.Errorf("aggregate inputs must belong to one review")
	}
	if input.GeneratedAt.IsZero() {
		return Review{}, fmt.Errorf("generated time is required")
	}
	if err := evidence.ValidateVerdicts(input.Verdicts, input.Candidates); err != nil {
		return Review{}, err
	}

	byID := make(map[string]protocol.CandidateFinding, len(input.Candidates.Findings))
	for _, candidate := range input.Candidates.Findings {
		byID[candidate.ID] = candidate
	}
	verdictByID := make(map[string]protocol.EvidenceVerdict, len(input.Verdicts.Verdicts))
	accepted := make([]protocol.CandidateFinding, 0, len(input.Candidates.Findings))
	questions := make([]HumanQuestion, 0)
	rejected := make([]RejectedCandidate, 0)
	for _, verdict := range input.Verdicts.Verdicts {
		verdictByID[verdict.CandidateID] = verdict
		candidate := byID[verdict.CandidateID]
		switch verdict.Verdict {
		case protocol.VerdictAccepted, protocol.VerdictDowngraded:
			candidate.Severity = *verdict.FinalSeverity
			accepted = append(accepted, candidate)
		case protocol.VerdictNeedsHuman:
			if verdict.FinalSeverity != nil {
				candidate.Severity = *verdict.FinalSeverity
			}
			questions = append(questions, HumanQuestion{Candidate: candidate, Reason: verdict.Reason})
		case protocol.VerdictRejected:
			rejected = append(rejected, RejectedCandidate{CandidateID: candidate.ID, Role: candidate.Role, Stage: "evidence-gate", ReasonCode: verdict.ReasonCode, Reason: verdict.Reason})
		}
	}
	for _, precheck := range input.PrecheckReports {
		for _, item := range precheck.Rejected {
			rejected = append(rejected, RejectedCandidate{CandidateID: item.CandidateID, Role: item.Role, Stage: "deterministic-precheck", ReasonCode: item.ReasonCode, Reason: item.Reason})
		}
	}

	groups := dedupe.GroupFindings(accepted)
	findings := make([]FinalFinding, 0, len(groups))
	canonicalIndex := make(map[string]int)
	for _, group := range groups {
		gateReason := verdictByID[group.Canonical.ID].Reason
		index := len(findings)
		findings = append(findings, FinalFinding{Candidate: group.Canonical, SourceRoles: append([]string{}, group.SourceRoles...), DuplicateIDs: append([]string{}, group.DuplicateIDs...), GateReason: gateReason})
		canonicalIndex[group.Canonical.ID] = index
		for _, member := range group.Members {
			canonicalIndex[member.ID] = index
		}
	}
	for _, verdict := range input.Verdicts.Verdicts {
		if verdict.Verdict != protocol.VerdictDuplicate || verdict.DuplicateOf == nil {
			continue
		}
		index, ok := canonicalIndex[*verdict.DuplicateOf]
		if !ok {
			return Review{}, fmt.Errorf("duplicate %q has no final canonical finding", verdict.CandidateID)
		}
		candidate := byID[verdict.CandidateID]
		findings[index].SourceRoles = appendUnique(findings[index].SourceRoles, candidate.Role)
		findings[index].DuplicateIDs = appendUnique(findings[index].DuplicateIDs, candidate.ID)
	}

	sort.SliceStable(findings, func(i, j int) bool { return better(findings[i].Candidate, findings[j].Candidate) })
	sort.Slice(questions, func(i, j int) bool { return better(questions[i].Candidate, questions[j].Candidate) })
	sort.Slice(rejected, func(i, j int) bool { return rejected[i].CandidateID < rejected[j].CandidateID })
	coverage := unique(input.CoverageLimits)
	status := "complete"
	if len(coverage) > 0 || input.Routing.Degraded {
		status = "complete-with-limits"
	}
	return Review{
		Version: Version, RunID: input.RunID, GeneratedAt: input.GeneratedAt.UTC(), Status: status,
		Scope: input.Scope, Routing: input.Routing, MaxParallel: input.MaxParallel, Roles: input.Roles,
		EvidenceStatus: input.EvidenceStatus, Findings: findings, NeedsHuman: questions,
		CoverageLimits: coverage, Rejected: rejected,
	}, nil
}

func RenderMarkdown(review Review) ([]byte, error) {
	if review.Version != Version || review.RunID == "" {
		return nil, fmt.Errorf("cannot render invalid review")
	}
	var output bytes.Buffer
	fmt.Fprintln(&output, "# Ревью Zephyr")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Область проверки")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Статус: %s\n", clean(review.Status))
	fmt.Fprintf(&output, "- Источник: %s\n", clean(string(review.Scope.Source)))
	fmt.Fprintf(&output, "- Репозиторий: %s\n", clean(review.Scope.Repository))
	if review.Scope.Branch != "" {
		fmt.Fprintf(&output, "- Ветка: %s\n", clean(review.Scope.Branch))
	}
	fmt.Fprintf(&output, "- Base: `%s`\n", code(review.Scope.BaseSHA))
	fmt.Fprintf(&output, "- Head: `%s`\n", code(review.Scope.HeadSHA))
	if review.Scope.MergeBase != "" {
		fmt.Fprintf(&output, "- Merge base: `%s`\n", code(review.Scope.MergeBase))
	}
	fmt.Fprintf(&output, "- Изменено файлов: %d\n", len(review.Scope.ChangedFiles))

	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Маршрутизация")
	fmt.Fprintln(&output)
	for _, decision := range review.Routing.Selected {
		fmt.Fprintf(&output, "- `%s` — %s\n", code(decision.Role), clean(strings.Join(decision.Reasons, "; ")))
	}
	fmt.Fprintf(&output, "\nПараллельность: %d. Evidence gate: %s.\n", review.MaxParallel, clean(review.EvidenceStatus))

	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Подтверждённые замечания")
	if len(review.Findings) == 0 {
		fmt.Fprintln(&output, "\nДоказуемых проблем в проверенной области не найдено.")
	}
	for _, finding := range review.Findings {
		candidate := finding.Candidate
		fmt.Fprintf(&output, "\n### [%s] %s\n\n", candidate.Severity, clean(candidate.Title))
		fmt.Fprintf(&output, "- Место: `%s`\n", code(location(candidate)))
		fmt.Fprintf(&output, "- Роли: %s\n", clean(strings.Join(finding.SourceRoles, ", ")))
		fmt.Fprintf(&output, "- Влияние: %s\n", clean(candidate.Impact))
		fmt.Fprintf(&output, "- Путь ошибки: %s\n", clean(candidate.Evidence.ExecutionPath))
		fmt.Fprintf(&output, "- Нарушенный инвариант: %s\n", clean(candidate.Evidence.ViolatedInvariant))
		fmt.Fprintf(&output, "- Проверенный контрпример: %s\n", clean(candidate.Evidence.FalsifierChecked))
		fmt.Fprintf(&output, "- Рекомендация: %s\n", clean(candidate.Recommendation))
		fmt.Fprintf(&output, "- Evidence gate: %s\n", clean(finding.GateReason))
	}

	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Требуется решение человека")
	if len(review.NeedsHuman) == 0 {
		fmt.Fprintln(&output, "\n- нет")
	}
	for _, question := range review.NeedsHuman {
		fmt.Fprintf(&output, "\n- %s (`%s`): %s\n", clean(question.Candidate.Title), code(location(question.Candidate)), clean(question.Reason))
	}

	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "## Покрытие")
	if len(review.CoverageLimits) == 0 {
		fmt.Fprintln(&output, "\n- Все выбранные роли завершены.")
	}
	for _, limitation := range review.CoverageLimits {
		fmt.Fprintf(&output, "\n- %s\n", clean(limitation))
	}
	if len(review.Rejected) > 0 {
		fmt.Fprintf(&output, "\nОтклонено кандидатов: %d. Полные причины сохранены в JSON-отчёте.\n", len(review.Rejected))
	}
	return output.Bytes(), nil
}

func location(candidate protocol.CandidateFinding) string {
	location := candidate.Location
	if location.LineEnd > 0 && location.LineEnd != location.LineStart {
		return fmt.Sprintf("%s:%d-%d", location.File, location.LineStart, location.LineEnd)
	}
	return fmt.Sprintf("%s:%d", location.File, location.LineStart)
}

func better(left, right protocol.CandidateFinding) bool {
	if left.Severity.Rank() != right.Severity.Rank() {
		return left.Severity.Rank() < right.Severity.Rank()
	}
	if left.Confidence != right.Confidence {
		return left.Confidence > right.Confidence
	}
	return left.ID < right.ID
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	sort.Strings(values)
	return values
}

func unique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
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

func clean(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	return strings.NewReplacer("`", "'", "|", "\\|").Replace(value)
}

func code(value string) string { return strings.ReplaceAll(strings.TrimSpace(value), "`", "'") }
