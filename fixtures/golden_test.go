package fixtures_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/report"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type goldenFixture struct {
	Version        int                       `json:"version"`
	ID             string                    `json:"id"`
	PlanItem       int                       `json:"plan_item"`
	Title          string                    `json:"title"`
	Description    string                    `json:"description"`
	Mode           string                    `json:"mode"`
	Source         string                    `json:"source"`
	Files          []fixtureFile             `json:"files"`
	ChangedFiles   []string                  `json:"changed_files"`
	Plan           *fixturePlan              `json:"plan"`
	RoutingSignals []string                  `json:"routing_signals"`
	ForceInclude   []string                  `json:"force_include"`
	ForceExclude   []string                  `json:"force_exclude"`
	ReviewerRoles  []string                  `json:"reviewer_roles"`
	Candidates     []schema.CandidateFinding `json:"candidates"`
	Verdicts       []schema.EvidenceVerdict  `json:"verdicts"`
	Expected       expectedResult            `json:"expected"`
}

type fixtureFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type fixturePlan struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type expectedResult struct {
	PrecheckAccepted   int               `json:"precheck_accepted"`
	PrecheckRejected   int               `json:"precheck_rejected"`
	Final              []expectedFinding `json:"final"`
	NeedsHuman         int               `json:"needs_human"`
	RejectedCandidates int               `json:"rejected_candidates"`
	RejectionReasons   []string          `json:"rejection_reasons"`
	HonestEmpty        bool              `json:"honest_empty"`
}

type expectedFinding struct {
	ID           string          `json:"id"`
	Severity     schema.Severity `json:"severity"`
	SourceRoles  []string        `json:"source_roles"`
	DuplicateIDs []string        `json:"duplicate_ids"`
}

func TestGoldenFixtures(t *testing.T) {
	requiredIDs := []string{
		"01-local-functional-go-bug",
		"02-context-loss",
		"03-concurrency-race",
		"04-auth-idor-risk",
		"05-unsafe-sql-migration",
		"06-broken-api-compatibility",
		"07-missing-negative-test",
		"08-plan-gap-no-diff",
		"09-plan-implementation-mismatch",
		"10-cross-role-duplicate",
		"11-rejected-false-positive",
		"12-clean-diff",
	}
	entries, err := os.ReadDir("golden")
	require.NoError(t, err)
	var fixturePaths []string
	for _, entry := range entries {
		if entry.IsDir() {
			fixturePaths = append(fixturePaths, filepath.Join("golden", entry.Name(), "fixture.json"))
		}
	}
	sort.Strings(fixturePaths)
	require.GreaterOrEqual(t, len(fixturePaths), 12, "AGENTS.md section 16.2 requires at least 12 golden fixtures")

	seenItems := make(map[int]string, len(fixturePaths))
	seenIDs := make(map[string]struct{}, len(fixturePaths))
	for _, path := range fixturePaths {
		fixture := loadFixture(t, path)
		if fixture.PlanItem > 0 {
			if previous := seenItems[fixture.PlanItem]; previous != "" {
				t.Fatalf("fixtures %q and %q both cover AGENTS.md item %d", previous, fixture.ID, fixture.PlanItem)
			}
			if required := requiredIDs[fixture.PlanItem-1]; fixture.ID != required {
				t.Fatalf("AGENTS.md item %d must be fixture %q, got %q", fixture.PlanItem, required, fixture.ID)
			}
			seenItems[fixture.PlanItem] = fixture.ID
		}
		_, duplicate := seenIDs[fixture.ID]
		require.Falsef(t, duplicate, "duplicate fixture ID %q", fixture.ID)
		seenIDs[fixture.ID] = struct{}{}

		t.Run(fixture.ID, func(t *testing.T) {
			runGolden(t, fixture)
		})
	}
	for item := 1; item <= 12; item++ {
		assert.NotEmptyf(t, seenItems[item], "missing fixture for AGENTS.md section 16.2 item %d", item)
	}
}

func loadFixture(t *testing.T, path string) goldenFixture {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var fixture goldenFixture
	require.NoErrorf(t, decoder.Decode(&fixture), "decode %s", path)
	var trailing any
	require.ErrorIsf(t, decoder.Decode(&trailing), io.EOF, "%s contains trailing JSON", path)
	require.Equalf(t, 1, fixture.Version, "%s has incomplete fixture metadata", path)
	require.NotEmptyf(t, fixture.ID, "%s has incomplete fixture metadata", path)
	require.NotEmptyf(t, fixture.Title, "%s has incomplete fixture metadata", path)
	require.NotEmptyf(t, fixture.Description, "%s has incomplete fixture metadata", path)
	require.GreaterOrEqual(t, fixture.PlanItem, 0, "%s has invalid AGENTS.md item", path)
	require.LessOrEqual(t, fixture.PlanItem, 12, "%s has invalid AGENTS.md item", path)
	assert.Contains(t, []string{"plan", "implementation", "alignment"}, fixture.Mode, "%s has unsupported resolved mode", path)
	require.NotEmptyf(t, fixture.ReviewerRoles, "%s must exercise at least one isolated reviewer output", path)
	return fixture
}

func runGolden(t *testing.T, fixture goldenFixture) {
	t.Helper()
	runID := "fixture-" + fixture.ID
	repositoryRoot := t.TempDir()
	writeRepositoryFiles(t, repositoryRoot, fixture.Files)

	packet := contextpack.Packet{
		Version:             contextpack.Version,
		RunID:               runID,
		Mode:                fixture.Mode,
		Source:              fixture.Source,
		Repository:          contextpack.Repository{Root: repositoryRoot, Branch: "fixture", Head: "0000000000000000000000000000000000000000"},
		GitMetadata:         json.RawMessage(`{"include_generated":false,"include_vendor":false}`),
		ChangedFiles:        append([]string(nil), fixture.ChangedFiles...),
		Technologies:        []string{"go"},
		BusinessContext:     []contextpack.BusinessSnapshot{},
		ProjectInstructions: []contextpack.Document{},
		RoutingSignals:      []string{},
		CoverageLimits:      []string{},
	}
	packet.Diff.Full = fixturePatch(fixture)
	packet.Diff.TotalBytes = int64(len(packet.Diff.Full))
	if fixture.Plan != nil {
		packet.Plan = &contextpack.Document{Kind: "plan", Path: fixture.Plan.Path, Content: fixture.Plan.Content}
	}

	cfg, err := config.Load("")
	require.NoError(t, err)
	routeResult, err := routing.Route(cfg, routing.Input{
		Mode:         routing.Mode(fixture.Mode),
		ChangedPaths: fixture.ChangedFiles,
		Signals:      fixture.RoutingSignals,
		HasPlan:      fixture.Plan != nil,
		HasChanges:   len(fixture.ChangedFiles) > 0,
		ForceInclude: fixture.ForceInclude,
		ForceExclude: fixture.ForceExclude,
	})
	require.NoError(t, err, "routing")
	if diff := cmp.Diff(fixture.ReviewerRoles, routedRoles(routeResult)); diff != "" {
		t.Fatalf("routed roles mismatch (-want +got):\n%s", diff)
	}
	reports := make([]evidence.PrecheckReport, 0, len(fixture.ReviewerRoles))
	declaredRoles := make(map[string]struct{}, len(fixture.ReviewerRoles))
	for _, role := range fixture.ReviewerRoles {
		if _, duplicate := declaredRoles[role]; duplicate {
			t.Fatalf("reviewer role %q is declared twice", role)
		}
		declaredRoles[role] = struct{}{}
		envelope := schema.CandidateEnvelope{Version: schema.ProtocolVersion, RunID: runID, Role: role, Findings: []schema.CandidateFinding{}}
		for _, candidate := range fixture.Candidates {
			if candidate.Role == role {
				envelope.Findings = append(envelope.Findings, candidate)
			}
		}
		data, err := json.Marshal(envelope)
		require.NoError(t, err)
		validated, err := schema.ValidateCandidateBytes(data)
		require.NoErrorf(t, err, "candidate schema validation for role %q", role)
		reports = append(reports, evidence.Precheck(validated, packet, cfg))
	}
	for _, candidate := range fixture.Candidates {
		if _, ok := declaredRoles[candidate.Role]; !ok {
			t.Fatalf("candidate %q belongs to undeclared role %q", candidate.ID, candidate.Role)
		}
	}

	accepted, rejected := precheckCounts(reports)
	if accepted != fixture.Expected.PrecheckAccepted || rejected != fixture.Expected.PrecheckRejected {
		t.Fatalf("precheck counts: got accepted=%d rejected=%d, want accepted=%d rejected=%d; reports=%#v", accepted, rejected, fixture.Expected.PrecheckAccepted, fixture.Expected.PrecheckRejected, reports)
	}
	candidateSet := evidence.MergeCandidateReports(runID, reports)
	verdictEnvelope := schema.EvidenceVerdictEnvelope{Version: schema.ProtocolVersion, RunID: runID, Verdicts: fixture.Verdicts}
	verdictData, err := json.Marshal(verdictEnvelope)
	require.NoError(t, err)
	validatedVerdicts, err := schema.ValidateVerdictBytes(verdictData)
	require.NoError(t, err, "verdict schema validation")
	require.NoError(t, evidence.ValidateVerdicts(validatedVerdicts, candidateSet), "verdict integrity")

	input := report.AggregateInput{
		RunID:       runID,
		GeneratedAt: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
		Scope: report.Scope{
			Mode: fixture.Mode, Source: fixture.Source, Repository: "fixture:" + fixture.ID,
			Branch: "fixture", Head: "0000000000000000000000000000000000000000",
			ChangedFiles: append([]string(nil), fixture.ChangedFiles...), Sources: []report.SourceProvenance{},
		},
		Routing:    report.RoutingSummary{Profile: "golden", Selected: selectedRoles(fixture.ReviewerRoles), Excluded: []report.RoleDecision{}, MaxParallel: 1},
		Candidates: candidateSet, Verdicts: validatedVerdicts, PrecheckReports: reports,
		RejectedPath: "rejected-findings.json",
	}
	if fixture.Plan != nil {
		input.Scope.Plan = fixture.Plan.Path
	}
	review, rejectedArtifact, err := report.Aggregate(input)
	require.NoError(t, err)
	assertExpected(t, review, rejectedArtifact, fixture.Expected)

	reviewAgain, rejectedAgain, err := report.Aggregate(input)
	require.NoError(t, err)
	assertJSONEqual(t, review, reviewAgain, "review aggregation")
	assertJSONEqual(t, rejectedArtifact, rejectedAgain, "rejected-candidate aggregation")

	markdown, err := report.RenderMarkdown(review, report.RenderOptions{MaxFinalFindings: cfg.Limits.MaxFinalFindings})
	require.NoError(t, err)
	markdownAgain, err := report.RenderMarkdown(reviewAgain, report.RenderOptions{MaxFinalFindings: cfg.Limits.MaxFinalFindings})
	require.NoError(t, err)
	assert.Equal(t, markdown, markdownAgain, "markdown rendering is not deterministic")
	containsHonestEmpty := strings.Contains(string(markdown), "Доказуемых проблем в проверенной области не найдено")
	assert.Equal(t, fixture.Expected.HonestEmpty, containsHonestEmpty)
}

func fixturePatch(fixture goldenFixture) string {
	contentByPath := make(map[string]string, len(fixture.Files))
	for _, file := range fixture.Files {
		contentByPath[file.Path] = file.Content
	}
	var builder strings.Builder
	for _, path := range fixture.ChangedFiles {
		content, ok := contentByPath[path]
		if !ok {
			continue
		}
		lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
		fmt.Fprintf(&builder, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n", path, path, path, path)
		fmt.Fprintf(&builder, "@@ -1,%d +1,%d @@\n", len(lines), len(lines))
		for _, line := range lines {
			builder.WriteByte(' ')
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	return builder.String()
}

func writeRepositoryFiles(t *testing.T, root string, files []fixtureFile) {
	t.Helper()
	for _, file := range files {
		clean := filepath.Clean(filepath.FromSlash(file.Path))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			t.Fatalf("fixture file escapes repository: %q", file.Path)
		}
		target := filepath.Join(root, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(file.Content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func precheckCounts(reports []evidence.PrecheckReport) (int, int) {
	var accepted, rejected int
	for _, item := range reports {
		accepted += len(item.Accepted)
		rejected += len(item.Rejected)
	}
	return accepted, rejected
}

func selectedRoles(roles []string) []report.RoleDecision {
	result := make([]report.RoleDecision, 0, len(roles))
	for _, role := range roles {
		result = append(result, report.RoleDecision{Role: role, Reasons: []string{"golden fixture"}})
	}
	return result
}

func routedRoles(result routing.Result) []string {
	roles := make([]string, 0, len(result.Selected))
	for _, decision := range result.Selected {
		roles = append(roles, decision.Role)
	}
	return roles
}

func assertExpected(t *testing.T, review report.Review, rejected report.RejectedArtifact, expected expectedResult) {
	t.Helper()
	if len(review.Findings) != len(expected.Final) {
		t.Fatalf("final finding count: got %d, want %d", len(review.Findings), len(expected.Final))
	}
	for index, want := range expected.Final {
		got := review.Findings[index]
		if got.Candidate.ID != want.ID || got.Candidate.Severity != want.Severity ||
			!slices.Equal(got.SourceRoles, want.SourceRoles) || !slices.Equal(got.DuplicateIDs, want.DuplicateIDs) {
			t.Fatalf("final finding[%d]: got id=%q severity=%q roles=%v duplicates=%v; want %+v", index, got.Candidate.ID, got.Candidate.Severity, got.SourceRoles, got.DuplicateIDs, want)
		}
	}
	if len(review.NeedsHuman) != expected.NeedsHuman {
		t.Fatalf("needs-human count: got %d, want %d", len(review.NeedsHuman), expected.NeedsHuman)
	}
	if len(rejected.Rejected) != expected.RejectedCandidates || review.Rejected.Count != expected.RejectedCandidates {
		t.Fatalf("rejected count: artifact=%d review=%d want=%d", len(rejected.Rejected), review.Rejected.Count, expected.RejectedCandidates)
	}
	var reasons []string
	for _, item := range rejected.Rejected {
		reasons = append(reasons, item.ReasonCode)
	}
	sort.Strings(reasons)
	wantReasons := append([]string(nil), expected.RejectionReasons...)
	sort.Strings(wantReasons)
	if diff := cmp.Diff(wantReasons, reasons); diff != "" {
		t.Fatalf("rejection reasons mismatch (-want +got):\n%s", diff)
	}
}

func assertJSONEqual(t *testing.T, left, right any, label string) {
	t.Helper()
	leftJSON, err := json.Marshal(left)
	require.NoError(t, err)
	rightJSON, err := json.Marshal(right)
	require.NoError(t, err)
	assert.Equal(t, leftJSON, rightJSON, "%s is not deterministic", label)
}

func TestFixtureIDsAreFilesystemSafe(t *testing.T) {
	entries, err := os.ReadDir("golden")
	require.NoError(t, err)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		fixture := loadFixture(t, filepath.Join("golden", entry.Name(), "fixture.json"))
		assert.Equal(t, entry.Name(), fixture.ID, "directory must match fixture ID")
		assert.NotContains(t, fixture.ID, "/", "unsafe fixture ID")
		assert.NotContains(t, fixture.ID, "\\", "unsafe fixture ID")
	}
}
