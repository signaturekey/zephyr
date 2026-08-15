package fixtures_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/protocol"
	"github.com/signaturekey/zephyr/internal/report"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/snapshot"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	Version      int                         `json:"version"`
	ID           string                      `json:"id"`
	Description  string                      `json:"description"`
	Files        []fixtureFile               `json:"files"`
	ChangedFiles []string                    `json:"changed_files"`
	Candidates   []protocol.CandidateFinding `json:"candidates"`
	Verdicts     []protocol.EvidenceVerdict  `json:"verdicts"`
	Expected     fixtureExpected             `json:"expected"`
}

type fixtureFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type fixtureExpected struct {
	PrecheckAccepted   int            `json:"precheck_accepted"`
	PrecheckRejected   int            `json:"precheck_rejected"`
	Final              []fixtureFinal `json:"final"`
	NeedsHuman         int            `json:"needs_human"`
	RejectedCandidates int            `json:"rejected_candidates"`
	RejectionReasons   []string       `json:"rejection_reasons"`
	HonestEmpty        bool           `json:"honest_empty"`
}

type fixtureFinal struct {
	ID           string            `json:"id"`
	Severity     protocol.Severity `json:"severity"`
	SourceRoles  []string          `json:"source_roles"`
	DuplicateIDs []string          `json:"duplicate_ids"`
}

func TestImplementationGoldenFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("golden", "*", "fixture.json"))
	require.NoError(t, err)
	sort.Strings(paths)
	cfg, err := config.LoadBytes(nil)
	require.NoError(t, err)
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			var input fixture
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			require.NoError(t, decoder.Decode(&input))
			require.Equal(t, protocol.ProtocolVersion, input.Version)
			require.Equal(t, filepath.Base(filepath.Dir(path)), input.ID)
			require.NotEmpty(t, input.Description)
			diff := fixtureDiff(input.Files)
			byRole := make(map[string][]protocol.CandidateFinding)
			for _, candidate := range input.Candidates {
				byRole[candidate.Role] = append(byRole[candidate.Role], candidate)
			}
			var prechecks []evidence.PrecheckReport
			for role, findings := range byRole {
				prechecks = append(prechecks, evidence.Precheck(protocol.CandidateEnvelope{Version: 1, RunID: input.ID, Role: role, Findings: findings}, evidence.Scope{
					RunID: input.ID, Diff: diff, ChangedFiles: input.ChangedFiles, Config: cfg,
				}))
			}
			accepted, rejected := 0, 0
			for _, precheck := range prechecks {
				accepted += len(precheck.Accepted)
				rejected += len(precheck.Rejected)
			}
			require.Equal(t, input.Expected.PrecheckAccepted, accepted)
			require.Equal(t, input.Expected.PrecheckRejected, rejected)
			candidates := evidence.MergeCandidateReports(input.ID, prechecks)
			verdicts := protocol.EvidenceVerdictEnvelope{Version: 1, RunID: input.ID, Verdicts: input.Verdicts}
			result, err := report.Aggregate(report.AggregateInput{
				RunID: input.ID, GeneratedAt: time.Unix(1, 0),
				Scope:   report.Scope{Source: snapshot.SourceWorktree, HeadSHA: "head", BaseSHA: "base", ChangedFiles: input.ChangedFiles},
				Routing: routing.Result{}, MaxParallel: 4, Candidates: candidates, Verdicts: verdicts,
				PrecheckReports: prechecks, EvidenceStatus: "validated",
			})
			require.NoError(t, err)
			actual := make([]fixtureFinal, 0, len(result.Findings))
			for _, finding := range result.Findings {
				actual = append(actual, fixtureFinal{ID: finding.Candidate.ID, Severity: finding.Candidate.Severity, SourceRoles: finding.SourceRoles, DuplicateIDs: finding.DuplicateIDs})
			}
			if diff := cmp.Diff(input.Expected.Final, actual); diff != "" {
				t.Fatalf("final findings mismatch (-want +got):\n%s", diff)
			}
			require.Len(t, result.NeedsHuman, input.Expected.NeedsHuman)
			require.Len(t, result.Rejected, input.Expected.RejectedCandidates)
			reasons := make([]string, 0, len(result.Rejected))
			for _, rejected := range result.Rejected {
				reasons = append(reasons, rejected.ReasonCode)
			}
			sort.Strings(reasons)
			require.Equal(t, input.Expected.RejectionReasons, reasons)
			require.Equal(t, input.Expected.HonestEmpty, len(result.Findings) == 0 && len(result.NeedsHuman) == 0)
		})
	}
}

func fixtureDiff(files []fixtureFile) string {
	var output strings.Builder
	for _, file := range files {
		lines := strings.Split(strings.TrimSuffix(file.Content, "\n"), "\n")
		fmt.Fprintf(&output, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", file.Path, file.Path, file.Path, len(lines))
		for _, line := range lines {
			output.WriteByte('+')
			output.WriteString(line)
			output.WriteByte('\n')
		}
	}
	return output.String()
}
