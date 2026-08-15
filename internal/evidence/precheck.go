package evidence

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/protocol"
)

type Scope struct {
	RunID        string
	Diff         string
	ChangedFiles []string
	Config       config.Config
}

type Rejection struct {
	CandidateID string `json:"candidate_id"`
	Role        string `json:"role"`
	ReasonCode  string `json:"reason_code"`
	Reason      string `json:"reason"`
}

type PrecheckReport struct {
	Version  int                         `json:"version"`
	RunID    string                      `json:"run_id"`
	Role     string                      `json:"role"`
	Accepted []protocol.CandidateFinding `json:"accepted"`
	Rejected []Rejection                 `json:"rejected"`
}

type CandidateSet struct {
	Version  int                         `json:"version"`
	RunID    string                      `json:"run_id"`
	Findings []protocol.CandidateFinding `json:"findings"`
}

func Precheck(envelope protocol.CandidateEnvelope, scope Scope) PrecheckReport {
	report := PrecheckReport{Version: protocol.ProtocolVersion, RunID: scope.RunID, Role: envelope.Role, Accepted: []protocol.CandidateFinding{}, Rejected: []Rejection{}}
	seen := make(map[string]struct{}, len(envelope.Findings))
	for _, finding := range envelope.Findings {
		code, reason := validateFinding(envelope, finding, scope, seen)
		if code != "" {
			report.Rejected = append(report.Rejected, Rejection{CandidateID: finding.ID, Role: finding.Role, ReasonCode: code, Reason: reason})
			continue
		}
		seen[finding.ID] = struct{}{}
		report.Accepted = append(report.Accepted, finding)
	}
	sortFindings(report.Accepted)
	sort.Slice(report.Rejected, func(i, j int) bool { return report.Rejected[i].CandidateID < report.Rejected[j].CandidateID })
	return report
}

func validateFinding(envelope protocol.CandidateEnvelope, finding protocol.CandidateFinding, scope Scope, seen map[string]struct{}) (string, string) {
	if envelope.Version != protocol.ProtocolVersion || envelope.RunID != scope.RunID {
		return "protocol-mismatch", "candidate envelope does not belong to this review"
	}
	if finding.Role != envelope.Role {
		return "role-mismatch", "finding role differs from isolated reviewer role"
	}
	if role, ok := scope.Config.Roles[finding.Role]; !ok || !role.Enabled {
		return "role-disabled", "finding came from an unknown or disabled role"
	}
	if !strings.HasPrefix(finding.ID, finding.Role+"-") {
		return "invalid-id", "finding ID must be prefixed with its reviewer role"
	}
	if _, duplicate := seen[finding.ID]; duplicate {
		return "duplicate-id", "candidate ID is duplicated"
	}
	if !finding.Severity.Valid() {
		return "invalid-severity", "severity is not part of the protocol"
	}
	if finding.Role == config.RoleCodeSimplifier && finding.Severity.Rank() < protocol.SeverityP2.Rank() {
		return "severity-not-allowed", "code-simplifier may emit only P2 or P3"
	}
	if code, reason := validateReviewerScope(finding); code != "" {
		return code, reason
	}
	if strings.TrimSpace(finding.Impact) == "" || strings.TrimSpace(finding.Evidence.ExecutionPath) == "" ||
		strings.TrimSpace(finding.Evidence.ViolatedInvariant) == "" || strings.TrimSpace(finding.Evidence.FalsifierChecked) == "" {
		return "evidence-incomplete", "impact, execution path, invariant and falsifier check are required"
	}
	if finding.NeedsHuman && finding.Severity.Rank() <= protocol.SeverityP1.Rank() {
		return "high-severity-unproven", "P0/P1 cannot depend on unresolved human confirmation"
	}
	if !finding.Location.IsCode() || finding.Location.IsArtifact() {
		return "invalid-location", "implementation findings require exactly one code location"
	}
	path := filepath.ToSlash(filepath.Clean(finding.Location.File))
	if filepath.IsAbs(finding.Location.File) || path == "." || path == ".." || strings.HasPrefix(path, "../") {
		return "invalid-path", "code location must be repository-relative"
	}
	if !contains(scope.ChangedFiles, path) {
		return "out-of-scope", "code location is outside the frozen changed paths"
	}
	if denied(path, scope.Config) {
		return "restricted-path", "code location is excluded by configuration"
	}
	if finding.Location.LineStart < 1 || (finding.Location.LineEnd != 0 && finding.Location.LineEnd < finding.Location.LineStart) {
		return "invalid-line", "code line range is invalid"
	}
	lineEnd := finding.Location.LineEnd
	if lineEnd == 0 {
		lineEnd = finding.Location.LineStart
	}
	if !diffContainsLineRange(scope.Diff, path, finding.Location.LineStart, lineEnd) {
		return "line-out-of-snapshot", "code line range is outside frozen diff hunks"
	}
	if finding.Severity.Rank() <= protocol.SeverityP1.Rank() {
		if finding.Evidence.Code == nil || strings.TrimSpace(*finding.Evidence.Code) == "" {
			return "high-severity-evidence-incomplete", "P0/P1 requires a concrete code fragment"
		}
		if !diffContainsEvidenceCode(scope.Diff, path, *finding.Evidence.Code) {
			return "evidence-code-not-in-snapshot", "P0/P1 code evidence is absent from the frozen diff"
		}
	}
	return "", ""
}

func denied(path string, cfg config.Config) bool {
	patterns := append(append([]string(nil), cfg.RestrictedPaths...), cfg.Redaction.DenyPatterns...)
	for _, pattern := range patterns {
		matched, err := doublestar.PathMatch(filepath.ToSlash(pattern), path)
		if err != nil || matched {
			return true
		}
	}
	return false
}

func MergeCandidateReports(runID string, reports []PrecheckReport) CandidateSet {
	set := CandidateSet{Version: protocol.ProtocolVersion, RunID: runID, Findings: []protocol.CandidateFinding{}}
	for _, report := range reports {
		if report.RunID == runID {
			set.Findings = append(set.Findings, report.Accepted...)
		}
	}
	sortFindings(set.Findings)
	return set
}

func sortFindings(findings []protocol.CandidateFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity.Rank() != findings[j].Severity.Rank() {
			return findings[i].Severity.Rank() < findings[j].Severity.Rank()
		}
		if findings[i].Location.File != findings[j].Location.File {
			return findings[i].Location.File < findings[j].Location.File
		}
		if findings[i].Location.LineStart != findings[j].Location.LineStart {
			return findings[i].Location.LineStart < findings[j].Location.LineStart
		}
		return findings[i].ID < findings[j].ID
	})
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if filepath.ToSlash(candidate) == value {
			return true
		}
	}
	return false
}
