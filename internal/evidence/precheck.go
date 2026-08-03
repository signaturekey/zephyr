package evidence

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/schema"
)

type Rejection struct {
	CandidateID string `json:"candidate_id"`
	Role        string `json:"role"`
	ReasonCode  string `json:"reason_code"`
	Reason      string `json:"reason"`
}

type PrecheckReport struct {
	Version  int                       `json:"version"`
	RunID    string                    `json:"run_id"`
	Role     string                    `json:"role"`
	Accepted []schema.CandidateFinding `json:"accepted"`
	Rejected []Rejection               `json:"rejected"`
}

type CandidateSet struct {
	Version  int                       `json:"version"`
	RunID    string                    `json:"run_id"`
	Findings []schema.CandidateFinding `json:"findings"`
}

func Precheck(envelope schema.CandidateEnvelope, packet contextpack.Packet, cfg config.Config) PrecheckReport {
	report := PrecheckReport{
		Version:  schema.ProtocolVersion,
		RunID:    envelope.RunID,
		Role:     envelope.Role,
		Accepted: []schema.CandidateFinding{},
		Rejected: []Rejection{},
	}
	seen := make(map[string]struct{}, len(envelope.Findings))
	for _, finding := range envelope.Findings {
		code, reason := precheckFinding(envelope, finding, packet, cfg, seen)
		if code != "" {
			report.Rejected = append(report.Rejected, Rejection{
				CandidateID: finding.ID,
				Role:        finding.Role,
				ReasonCode:  code,
				Reason:      reason,
			})
			continue
		}
		seen[finding.ID] = struct{}{}
		report.Accepted = append(report.Accepted, finding)
	}
	sortFindings(report.Accepted)
	sort.Slice(report.Rejected, func(i, j int) bool {
		return report.Rejected[i].CandidateID < report.Rejected[j].CandidateID
	})
	return report
}

func precheckFinding(envelope schema.CandidateEnvelope, finding schema.CandidateFinding, packet contextpack.Packet, cfg config.Config, seen map[string]struct{}) (string, string) {
	if envelope.Version != schema.ProtocolVersion || packet.Version != contextpack.Version {
		return "protocol-mismatch", "candidate and packet protocol versions must match the core"
	}
	if envelope.RunID != packet.RunID {
		return "run-mismatch", "candidate envelope belongs to a different run"
	}
	if finding.Role != envelope.Role {
		return "role-mismatch", "finding role differs from its isolated reviewer role"
	}
	role, known := cfg.Roles[finding.Role]
	if !known || !role.Enabled {
		return "role-disabled", "finding was produced by an unknown or disabled role"
	}
	if !strings.HasPrefix(finding.ID, finding.Role+"-") {
		return "invalid-id", "finding ID must be prefixed with the reviewer role"
	}
	if _, duplicate := seen[finding.ID]; duplicate {
		return "duplicate-id", "candidate ID is duplicated in one reviewer output"
	}
	if !finding.Severity.Valid() {
		return "invalid-severity", "finding severity is not part of the protocol"
	}
	if finding.Role == config.RoleCodeSimplifier && finding.Severity.Rank() < schema.SeverityP2.Rank() {
		return "severity-not-allowed", "code-simplifier may emit only P2 or P3"
	}
	if code, reason := validateReviewerScope(finding); code != "" {
		return code, reason
	}
	if finding.NeedsHuman && finding.Severity.Rank() <= schema.SeverityP1.Rank() {
		return "high-severity-unproven", "P0/P1 cannot depend on unresolved human confirmation"
	}
	if strings.TrimSpace(finding.Impact) == "" || strings.TrimSpace(finding.Evidence.ExecutionPath) == "" ||
		strings.TrimSpace(finding.Evidence.ViolatedInvariant) == "" || strings.TrimSpace(finding.Evidence.FalsifierChecked) == "" {
		return "evidence-incomplete", "impact, execution path, invariant, and falsifier check are required"
	}
	if finding.Severity.Rank() <= schema.SeverityP1.Rank() {
		if finding.Location.IsCode() && (finding.Evidence.Code == nil || strings.TrimSpace(*finding.Evidence.Code) == "") {
			return "high-severity-evidence-incomplete", "code P0/P1 requires a concrete code or diff fragment"
		}
	}

	switch {
	case finding.Location.IsCode() && !finding.Location.IsArtifact():
		if code, reason := precheckCodeLocation(finding.Location, packet, cfg); code != "" {
			return code, reason
		}
		if finding.Severity.Rank() <= schema.SeverityP1.Rank() &&
			!diffContainsEvidenceCode(packet.Diff.Full, filepath.ToSlash(filepath.Clean(finding.Location.File)), *finding.Evidence.Code) {
			return "evidence-code-not-in-snapshot", "code P0/P1 evidence fragment is not present in the immutable diff"
		}
		return "", ""
	case finding.Location.IsArtifact() && !finding.Location.IsCode():
		return precheckArtifactLocation(finding.Location, packet)
	default:
		return "invalid-location", "finding must have exactly one code or artifact location"
	}
}

func precheckCodeLocation(location schema.FindingLocation, packet contextpack.Packet, cfg config.Config) (string, string) {
	if packet.Mode == "plan" {
		return "out-of-scope", "plan-only review cannot emit a live code location"
	}
	path := filepath.ToSlash(filepath.Clean(location.File))
	if filepath.IsAbs(location.File) || path == ".." || strings.HasPrefix(path, "../") {
		return "invalid-path", "code location must be repository-relative"
	}
	if !contains(packet.ChangedFiles, path) {
		return "out-of-scope", "code location is outside the changed-file snapshot"
	}
	if deniedPath(path, cfg, packet.GitMetadata) {
		return "restricted-path", "code location is excluded by restricted or redaction policy"
	}
	if location.LineStart < 1 || (location.LineEnd != 0 && location.LineEnd < location.LineStart) {
		return "invalid-line", "code line range is invalid"
	}

	lineEnd := location.LineEnd
	if lineEnd == 0 {
		lineEnd = location.LineStart
	}
	if !diffContainsLineRange(packet.Diff.Full, path, location.LineStart, lineEnd) {
		return "line-out-of-snapshot", "code line range is not present in the immutable diff hunks"
	}
	return "", ""
}

func precheckArtifactLocation(location schema.FindingLocation, packet contextpack.Packet) (string, string) {
	if packet.Mode == "implementation" {
		return "out-of-scope", "implementation-only review cannot emit a plan artifact location"
	}
	if packet.Plan == nil {
		return "missing-artifact", "packet does not contain a plan artifact"
	}
	artifact := filepath.ToSlash(filepath.Clean(location.Artifact))
	planPath := filepath.ToSlash(filepath.Clean(packet.Plan.Path))
	if artifact != planPath && artifact != filepath.Base(planPath) && !strings.HasSuffix(planPath, "/"+artifact) {
		return "artifact-mismatch", "finding references an artifact outside the snapshotted plan"
	}
	if strings.TrimSpace(location.Section) == "" {
		return "invalid-section", "plan finding must identify a section or a missing required section"
	}
	if location.LineStart < 0 || (location.LineEnd != 0 && location.LineEnd < location.LineStart) {
		return "invalid-line", "artifact line range is invalid"
	}
	if location.LineStart > 0 {
		lineCount := strings.Count(packet.Plan.Content, "\n") + 1
		lineEnd := location.LineEnd
		if lineEnd == 0 {
			lineEnd = location.LineStart
		}
		if location.LineStart > lineCount || lineEnd > lineCount {
			return "line-out-of-range", fmt.Sprintf("artifact line range exceeds snapshotted length %d", lineCount)
		}
	}
	return "", ""
}

func deniedPath(path string, cfg config.Config, metadata json.RawMessage) bool {
	includeGenerated := false
	includeVendor := false
	var flags struct {
		IncludeGenerated bool `json:"include_generated"`
		IncludeVendor    bool `json:"include_vendor"`
	}
	_ = json.Unmarshal(metadata, &flags)
	includeGenerated = flags.IncludeGenerated
	includeVendor = flags.IncludeVendor

	for _, pattern := range cfg.Redaction.DenyPatterns {
		matched, err := doublestar.PathMatch(filepath.ToSlash(pattern), path)
		if err != nil || matched {
			return true
		}
	}
	for _, pattern := range cfg.RestrictedPaths {
		if includeGenerated && generatedPath(path) && strings.Contains(pattern, "generated") {
			continue
		}
		if includeVendor && vendorPath(path) && strings.Contains(pattern, "vendor") {
			continue
		}
		matched, err := doublestar.PathMatch(filepath.ToSlash(pattern), path)
		if err != nil || matched {
			return true
		}
	}
	return false
}

func generatedPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(lower)
	return strings.HasPrefix(lower, "generated/") || strings.Contains(lower, "/generated/") ||
		strings.HasPrefix(lower, "__generated__/") || strings.Contains(lower, "/__generated__/") ||
		strings.HasSuffix(base, ".gen.go") || strings.HasSuffix(base, "_generated.go") || strings.HasSuffix(base, ".pb.go") ||
		strings.HasSuffix(base, ".gen.ts") || strings.HasSuffix(base, ".generated.ts") ||
		strings.HasSuffix(base, ".gen.tsx") || strings.HasSuffix(base, ".generated.tsx")
}

func vendorPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return strings.HasPrefix(lower, "vendor/") || strings.Contains(lower, "/vendor/")
}

func MergeCandidateReports(runID string, reports []PrecheckReport) CandidateSet {
	set := CandidateSet{Version: schema.ProtocolVersion, RunID: runID, Findings: []schema.CandidateFinding{}}
	for _, report := range reports {
		if report.RunID == runID {
			set.Findings = append(set.Findings, report.Accepted...)
		}
	}
	sortFindings(set.Findings)
	return set
}

func MergeRejections(runID string, reports []PrecheckReport) struct {
	Version  int         `json:"version"`
	RunID    string      `json:"run_id"`
	Rejected []Rejection `json:"rejected"`
} {
	result := struct {
		Version  int         `json:"version"`
		RunID    string      `json:"run_id"`
		Rejected []Rejection `json:"rejected"`
	}{Version: schema.ProtocolVersion, RunID: runID, Rejected: []Rejection{}}
	for _, report := range reports {
		if report.RunID == runID {
			result.Rejected = append(result.Rejected, report.Rejected...)
		}
	}
	sort.Slice(result.Rejected, func(i, j int) bool { return result.Rejected[i].CandidateID < result.Rejected[j].CandidateID })
	return result
}

func sortFindings(findings []schema.CandidateFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Severity.Rank() != findings[j].Severity.Rank() {
			return findings[i].Severity.Rank() < findings[j].Severity.Rank()
		}
		if findings[i].Location.File != findings[j].Location.File {
			return findings[i].Location.File < findings[j].Location.File
		}
		if findings[i].Location.Artifact != findings[j].Location.Artifact {
			return findings[i].Location.Artifact < findings[j].Location.Artifact
		}
		if findings[i].Location.LineStart != findings[j].Location.LineStart {
			return findings[i].Location.LineStart < findings[j].Location.LineStart
		}
		return findings[i].ID < findings[j].ID
	})
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if filepath.ToSlash(filepath.Clean(value)) == target {
			return true
		}
	}
	return false
}
