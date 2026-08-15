package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/signaturekey/zephyr/internal/agent"
	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/protocol"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/snapshot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRuntime struct {
	mu       sync.Mutex
	gateRuns int
}

func (runtime *fakeRuntime) Route(_ context.Context, request routing.Request, _ *snapshot.Snapshot, _ []agent.ContextDocument) (protocol.SemanticRoutingEnvelope, error) {
	result := protocol.SemanticRoutingEnvelope{Version: 1, RunID: request.RunID}
	for _, candidate := range request.Candidates {
		result.Decisions = append(result.Decisions, protocol.SemanticRoutingDecision{Role: candidate.Role, Decision: "exclude", EvidenceRefs: []string{"snapshot.diff"}, Reason: "not relevant", Confidence: 1})
	}
	return result, nil
}

func (runtime *fakeRuntime) Review(_ context.Context, runID, role string, _ *snapshot.Snapshot, _ []agent.ContextDocument) (protocol.CandidateEnvelope, error) {
	envelope := protocol.CandidateEnvelope{Version: 1, RunID: runID, Role: role, Findings: []protocol.CandidateFinding{}}
	if role != config.RoleCodeReviewer {
		return envelope, nil
	}
	code := "const value = 2"
	envelope.Findings = append(envelope.Findings, protocol.CandidateFinding{
		ID: role + "-001", Role: role, Severity: protocol.SeverityP1, Category: "correctness", Title: "invalid value",
		Location: protocol.FindingLocation{File: "main.go", LineStart: 3},
		Evidence: protocol.FindingEvidence{Code: &code, ExecutionPath: "changed constant is consumed", ViolatedInvariant: "value must stay one", FalsifierChecked: "no caller correction"},
		Impact:   "consumer fails", Recommendation: "restore valid value", Confidence: 0.9,
	})
	return envelope, nil
}

func (runtime *fakeRuntime) Gate(_ context.Context, runID string, candidates []protocol.CandidateFinding, _ *snapshot.Snapshot, _ []agent.ContextDocument) (protocol.EvidenceVerdictEnvelope, error) {
	runtime.mu.Lock()
	runtime.gateRuns++
	runtime.mu.Unlock()
	result := protocol.EvidenceVerdictEnvelope{Version: 1, RunID: runID}
	for _, candidate := range candidates {
		severity := candidate.Severity
		result.Verdicts = append(result.Verdicts, protocol.EvidenceVerdict{CandidateID: candidate.ID, Verdict: protocol.VerdictAccepted, FinalSeverity: &severity, ReasonCode: "evidence-complete", Reason: "supported"})
	}
	return result, nil
}

func (runtime *fakeRuntime) Close() error { return nil }

func TestServiceRunsOneSnapshotParallelRolesGateAndReport(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	git(t, repo, "config", "user.email", "test@example.com")
	git(t, repo, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "main.go"), []byte("package demo\n\nconst value = 1\n"), 0o644))
	git(t, repo, "add", "main.go")
	git(t, repo, "commit", "-m", "initial")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "main.go"), []byte("package demo\n\nconst value = 2\n"), 0o644))

	runtime := &fakeRuntime{}
	service := Service{RuntimeFactory: func(context.Context, config.Config) (agent.Runtime, error) { return runtime, nil }}
	result, err := service.Run(context.Background(), Request{Repository: repo, Source: snapshot.SourceWorktree, MaxParallel: 3})
	require.NoError(t, err)
	assert.Len(t, result.Review.Findings, 1)
	assert.Equal(t, "validated", result.Review.EvidenceStatus)
	assert.Contains(t, string(result.Markdown), "invalid value")
	assert.Contains(t, string(result.JSON), `"evidence_status": "validated"`)
	runtime.mu.Lock()
	assert.Equal(t, 1, runtime.gateRuns)
	runtime.mu.Unlock()
}

func git(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
