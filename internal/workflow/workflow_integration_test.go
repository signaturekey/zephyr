package workflow_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/gitcontext"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/signaturekey/zephyr/internal/trace"
	"github.com/signaturekey/zephyr/internal/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectFreezesResolvedModelPolicy(t *testing.T) {
	repository := newRepository(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repository, ".zephyr"), 0o700))
	writeFile(t, filepath.Join(repository, ".zephyr", "config.yaml"), `version: 1
model_policy:
  default:
    model: gpt-5.6-terra
    effort: medium
  stages:
    reviewers:
      default:
        fast: true
      roles:
        security-auditor:
          model: gpt-5.6-sol
          effort: xhigh
`)
	writeFile(t, filepath.Join(repository, "main.go"), "package example\n")

	service, err := workflow.New(t.TempDir())
	require.NoError(t, err)
	initialized, err := service.Init(context.Background(), workflow.InitOptions{
		Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree,
	})
	require.NoError(t, err)
	collected, err := service.Collect(context.Background(), workflow.CollectOptions{RunID: initialized.RunID})
	require.NoError(t, err)
	policy, err := os.ReadFile(collected.ModelPolicyPath)
	require.NoError(t, err)
	if strings.Contains(collected.ModelPolicyPath, repository) {
		t.Fatalf("model policy path leaked into repository: %q", collected.ModelPolicyPath)
	}
	if got, want := collected.ModelPolicySHA256, fmt.Sprintf("%x", sha256.Sum256(policy)); got != want {
		t.Fatalf("model policy hash = %q, want %q", got, want)
	}
	for _, want := range []string{
		"probe\t-\tgpt-5.6-luna\tlow\ttrue",
		"reviewer\tcode-reviewer\tgpt-5.6-terra\tmedium\ttrue",
		"reviewer\tsecurity-auditor\tgpt-5.6-sol\txhigh\ttrue",
		"evidence-gate\t-\tgpt-5.6-luna\thigh\tfalse",
	} {
		if !strings.Contains(string(policy), want) {
			t.Fatalf("frozen policy lacks %q:\n%s", want, policy)
		}
	}

	inspected, err := service.Inspect(context.Background(), initialized.RunID)
	require.NoError(t, err)
	if inspected.Artifacts.ModelPolicy != collected.ModelPolicyPath {
		t.Fatalf("inspect model policy = %q, want %q", inspected.Artifacts.ModelPolicy, collected.ModelPolicyPath)
	}
}

func TestImplementationLifecycleProducesEvidenceGatedReportWithoutDirtyingRepository(t *testing.T) {
	repository := newRepository(t)
	mainPath := filepath.Join(repository, "main.go")
	writeFile(t, mainPath, "package example\n\nfunc add(left, right int) int { return left - right }\n")
	statusBefore := gitOutput(t, repository, "status", "--porcelain=v1", "--untracked-files=all")

	service, err := workflow.New(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{
		Repository: repository,
		Mode:       run.ModeAuto,
		Source:     run.SourceWorkingTree,
	})
	require.NoError(t, err)
	collected, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID})
	if err != nil {
		t.Fatal(err)
	}
	if collected.Mode != run.ModeImplementation || !collected.Reviewable {
		t.Fatalf("unexpected collection result: %#v", collected)
	}
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(routed.Routing.Selected) == 0 || routed.Routing.Selected[0].Role != "code-reviewer" {
		t.Fatalf("mandatory implementation reviewer missing: %#v", routed.Routing.Selected)
	}

	code := "return left - right"
	for _, decision := range routed.Routing.Selected {
		envelope := schema.CandidateEnvelope{
			Version:  schema.ProtocolVersion,
			RunID:    initialized.RunID,
			Role:     decision.Role,
			Findings: []schema.CandidateFinding{},
		}
		if decision.Role == "code-reviewer" {
			envelope.Findings = []schema.CandidateFinding{{
				ID:       "code-reviewer-001",
				Role:     decision.Role,
				Severity: schema.SeverityP1,
				Category: "functional-correctness",
				Title:    "Addition now subtracts",
				Location: schema.FindingLocation{File: "main.go", LineStart: 3, LineEnd: 3, Symbol: "add"},
				Evidence: schema.FindingEvidence{
					Code:              &code,
					ExecutionPath:     "every caller of add reaches the changed return expression",
					ViolatedInvariant: "add must return the sum of its two operands",
					RequirementSource: nil,
					FalsifierChecked:  "checked for a compensating sign conversion and found none",
				},
				Impact:         "all non-zero calls return the wrong value",
				Recommendation: "restore addition and add a focused regression test",
				Confidence:     0.99,
				NeedsHuman:     false,
			}}
		}
		data := marshalJSON(t, envelope)
		if _, err := service.ValidateCandidates(ctx, workflow.ValidateCandidatesOptions{
			RunID: initialized.RunID, Role: decision.Role, Input: data,
		}); err != nil {
			t.Fatalf("validate %s: %v", decision.Role, err)
		}
	}
	severity := schema.SeverityP1
	verdicts := schema.EvidenceVerdictEnvelope{
		Version: schema.ProtocolVersion,
		RunID:   initialized.RunID,
		Verdicts: []schema.EvidenceVerdict{{
			CandidateID:   "code-reviewer-001",
			Verdict:       schema.VerdictAccepted,
			FinalSeverity: &severity,
			ReasonCode:    "evidence-complete",
			Reason:        "the immutable diff and execution path directly establish the regression",
			DuplicateOf:   nil,
		}},
	}
	if _, err := service.ValidateVerdicts(ctx, workflow.ValidateVerdictsOptions{
		RunID: initialized.RunID, Input: marshalJSON(t, verdicts),
	}); err != nil {
		t.Fatal(err)
	}
	aggregated, err := service.Aggregate(ctx, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if aggregated.Findings != 1 || aggregated.Stale {
		t.Fatalf("unexpected aggregate: %#v", aggregated)
	}
	rendered, err := service.Render(ctx, initialized.RunID, false)
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := os.ReadFile(rendered.ReviewMD)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdown), "Addition now subtracts") {
		t.Fatalf("rendered report lacks confirmed finding:\n%s", markdown)
	}
	reviewJSON, err := os.ReadFile(rendered.ReviewJSON)
	if err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string][]byte{"review.json": reviewJSON, "review.md": markdown} {
		if strings.Contains(string(output), repository) {
			t.Fatalf("%s leaked an absolute repository root", name)
		}
		logicalOutput := strings.ReplaceAll(string(output), "\\", "")
		if !strings.Contains(logicalOutput, "reviewed-repository") {
			t.Fatalf("%s lacks logical repository label", name)
		}
	}
	inspected, err := service.Inspect(ctx, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.State != run.StateComplete || inspected.Counts.ConfirmedFindings != 1 {
		t.Fatalf("unexpected final run: %#v", inspected)
	}
	statusAfter := gitOutput(t, repository, "status", "--porcelain=v1", "--untracked-files=all")
	if statusAfter != statusBefore {
		t.Fatalf("Zephyr dirtied the repository:\nbefore=%q\nafter=%q", statusBefore, statusAfter)
	}
}

func TestInvalidEvidenceGateMakesRunIncomplete(t *testing.T) {
	repository := newRepository(t)
	planPath := filepath.Join(repository, "REVIEW_SPEC.md")
	writeFile(t, planPath, "# Plan\n\nKeep the operation read-only.\n")
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{
		Repository: repository, Mode: run.ModePlan, Source: run.SourcePlanOnly, PlanPath: planPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range routed.Routing.Selected {
		envelope := schema.CandidateEnvelope{
			Version: schema.ProtocolVersion, RunID: initialized.RunID, Role: decision.Role,
			Findings: []schema.CandidateFinding{},
		}
		if _, err := service.ValidateCandidates(ctx, workflow.ValidateCandidatesOptions{
			RunID: initialized.RunID, Role: decision.Role, Input: marshalJSON(t, envelope),
		}); err != nil {
			t.Fatal(err)
		}
	}
	invalid := schema.EvidenceVerdictEnvelope{
		Version: schema.ProtocolVersion,
		RunID:   initialized.RunID,
		Verdicts: []schema.EvidenceVerdict{{
			CandidateID: "invented-001", Verdict: schema.VerdictRejected,
			ReasonCode: "out-of-scope", Reason: "not in the candidate set",
		}},
	}
	if _, err := service.ValidateVerdicts(ctx, workflow.ValidateVerdictsOptions{
		RunID: initialized.RunID, Input: marshalJSON(t, invalid),
	}); err == nil {
		t.Fatal("invalid evidence verdict unexpectedly succeeded")
	}
	inspected, err := service.Inspect(ctx, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.State != run.StateIncomplete {
		t.Fatalf("evidence failure state = %q, want incomplete", inspected.State)
	}
	if _, err := service.Aggregate(ctx, initialized.RunID); err == nil {
		t.Fatal("aggregate unexpectedly accepted a failed evidence gate")
	}
}

func TestRouteRequiresCompleteCapabilityPreflight(t *testing.T) {
	repository := newRepository(t)
	writeFile(t, filepath.Join(repository, "main.go"), "package example\n\nfunc add(left, right int) int { return left - right }\n")
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{
		Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCapability(ctx, workflow.CapabilitySetOptions{
		RunID: initialized.RunID, Source: workflow.CapabilityJira, Status: workflow.CapabilityAvailable,
	}); err == nil || !strings.Contains(err.Error(), "requires a completed Git collection") {
		t.Fatalf("early capability error = %v", err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddContext(ctx, workflow.ContextAddOptions{
		RunID: initialized.RunID, Source: "jira", Key: "RINT-1", Content: []byte("requirements"),
	}); err == nil || !strings.Contains(err.Error(), "requires a recorded capability") {
		t.Fatalf("context without capability error = %v", err)
	}
	if _, err := service.Route(ctx, workflow.RouteOptions{RunID: initialized.RunID}); err == nil ||
		!strings.Contains(err.Error(), "record jira, confluence, bitbucket") {
		t.Fatalf("incomplete preflight route error = %v", err)
	}
	if _, err := service.SetCapability(ctx, workflow.CapabilitySetOptions{
		RunID: initialized.RunID, Source: workflow.CapabilityJira, Status: workflow.CapabilityAvailable,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCapability(ctx, workflow.CapabilitySetOptions{
		RunID: initialized.RunID, Source: workflow.CapabilityConfluence, Status: workflow.CapabilityUnavailable,
	}); err == nil || !strings.Contains(err.Error(), "requires a reason") {
		t.Fatalf("reasonless unavailable capability error = %v", err)
	}
	if _, err := service.SetCapability(ctx, workflow.CapabilitySetOptions{
		RunID: initialized.RunID, Source: workflow.CapabilityConfluence, Status: workflow.CapabilityUnavailable,
		Reason: "read-only MCP is not callable",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetCapability(ctx, workflow.CapabilitySetOptions{
		RunID: initialized.RunID, Source: workflow.CapabilityBitbucket, Status: workflow.CapabilityNotRequired,
		Reason: "review uses the local Git snapshot",
	}); err != nil {
		t.Fatal(err)
	}
	routed, err := service.Route(ctx, workflow.RouteOptions{RunID: initialized.RunID})
	if err != nil {
		t.Fatal(err)
	}
	var packet contextpack.Packet
	readJSONFile(t, routed.PacketPath, &packet)
	if !slices.Contains(packet.Sources.Unavailable, contextpack.CoverageLimit{
		Source: "mcp:confluence", Reason: "read-only MCP is not callable",
	}) {
		t.Fatalf("capability coverage is absent from packet: %#v", packet.Sources.Unavailable)
	}
	if _, err := service.SetCapability(ctx, workflow.CapabilitySetOptions{
		RunID: initialized.RunID, Source: workflow.CapabilityBitbucket, Status: workflow.CapabilityAvailable,
	}); err == nil || !strings.Contains(err.Error(), "frozen before routing") {
		t.Fatalf("late capability error = %v", err)
	}
	inspected, err := service.Inspect(ctx, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspected.Capabilities) != 3 ||
		!slices.Contains(inspected.CoverageLimits, "mcp:confluence: read-only MCP is not callable") {
		t.Fatalf("inspect lacks capability preflight: %#v", inspected)
	}
}

func TestAllReviewerFailuresMakeRunIncomplete(t *testing.T) {
	repository := newRepository(t)
	planPath := filepath.Join(repository, "REVIEW_SPEC.md")
	writeFile(t, planPath, "# Plan\n\nDocument the rollout and rollback.\n")
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{
		Repository: repository, Mode: run.ModePlan, Source: run.SourcePlanOnly, PlanPath: planPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddContext(ctx, workflow.ContextAddOptions{
		RunID: initialized.RunID, Source: "jira", Key: "RINT-1", Content: []byte("too early"),
	}); err == nil {
		t.Fatal("business context unexpectedly succeeded before collection")
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	for _, capability := range []workflow.CapabilitySetOptions{
		{RunID: initialized.RunID, Source: workflow.CapabilityJira, Status: workflow.CapabilityUnavailable, Reason: "MCP unavailable"},
		{RunID: initialized.RunID, Source: workflow.CapabilityConfluence, Status: workflow.CapabilityAvailable},
		{RunID: initialized.RunID, Source: workflow.CapabilityBitbucket, Status: workflow.CapabilityNotRequired, Reason: "no PR context"},
	} {
		if _, err := service.SetCapability(ctx, capability); err != nil {
			t.Fatal(err)
		}
	}
	contextResult, err := service.AddContext(ctx, workflow.ContextAddOptions{
		RunID: initialized.RunID, Source: "confluence", Key: "DOC-42", URL: "https://docs.example.invalid/DOC-42", Content: []byte("Rollout requirements"),
	})
	if err != nil {
		t.Fatal(err)
	}
	routed, err := service.Route(ctx, workflow.RouteOptions{RunID: initialized.RunID})
	if err != nil {
		t.Fatal(err)
	}
	finalized, err := service.FallbackRouting(ctx, workflow.FinalizeRoutingOptions{
		RunID: initialized.RunID, Reason: "workflow fixture uses deterministic fallback",
	})
	if err != nil {
		t.Fatal(err)
	}
	routed.Routing = finalized.Routing
	routed.RoutingPath = finalized.RoutingPath
	if _, err := service.AddCoverage(ctx, workflow.CoverageAddOptions{
		RunID: initialized.RunID, Source: "late", Reason: "must not alter a frozen packet",
	}); err == nil {
		t.Fatal("context coverage unexpectedly changed after routing")
	}
	for _, decision := range routed.Routing.Selected {
		if _, err := service.MarkFailed(ctx, workflow.MarkFailedOptions{
			RunID: initialized.RunID, Stage: "review", Role: decision.Role, Reason: "reviewer timed out",
		}); err != nil {
			t.Fatalf("mark %s failed: %v", decision.Role, err)
		}
	}
	if _, err := service.ValidateVerdicts(ctx, workflow.ValidateVerdictsOptions{
		RunID: initialized.RunID,
		Input: marshalJSON(t, schema.EvidenceVerdictEnvelope{
			Version: schema.ProtocolVersion, RunID: initialized.RunID, Verdicts: []schema.EvidenceVerdict{},
		}),
	}); err == nil || !strings.Contains(err.Error(), "no selected reviewer produced a validated result") {
		t.Fatalf("ValidateVerdicts error = %v", err)
	}
	if _, err := service.Aggregate(ctx, initialized.RunID); err == nil {
		t.Fatal("aggregate unexpectedly accepted a run without validated reviewers")
	}
	if _, err := service.Render(ctx, initialized.RunID, false); err == nil {
		t.Fatal("render unexpectedly accepted a run without validated reviewers")
	}
	inspected, err := service.Inspect(ctx, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.State != run.StateIncomplete || inspected.Counts.FailedRoles != len(routed.Routing.Selected) {
		t.Fatalf("unexpected degraded run: %#v", inspected)
	}
	if inspected.Counts.ValidatedRoles != 0 || !slices.Contains(inspected.CoverageLimits, "evidence-gate: no selected reviewer produced a validated result") {
		t.Fatalf("incomplete run lacks zero-reviewer evidence: %#v", inspected)
	}
	if contextResult.ContentHash == "" {
		t.Fatal("business context was not snapshotted")
	}
}

func TestProjectRestrictedPathsNeverEnterDiffOrPacket(t *testing.T) {
	repository := newRepository(t)
	if err := os.MkdirAll(filepath.Join(repository, ".zephyr"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repository, ".zephyr", "config.yaml"), "version: 1\nrestricted_paths:\n  - private/**\n")
	writeFile(t, filepath.Join(repository, "private", "details.go"), "package private\n\nconst detail = \"placeholder\"\n")
	gitRun(t, repository, "add", ".zephyr/config.yaml", "private/details.go")
	gitRun(t, repository, "commit", "-m", "add review policy")
	const sentinel = "ULTRA_PRIVATE_VALUE_73492"
	writeFile(t, filepath.Join(repository, "private", "details.go"), "package private\n\nconst detail = \""+sentinel+"\"\n")
	writeFile(t, filepath.Join(repository, "main.go"), "package example\n\nfunc add(left, right int) int { return left - right }\n")

	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{
		Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree,
	})
	if err != nil {
		t.Fatal(err)
	}
	collected, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot gitcontext.Snapshot
	readJSONFile(t, collected.SnapshotPath, &snapshot)
	if strings.Contains(snapshot.Patches.Full, sentinel) {
		t.Fatal("project-restricted content leaked into the immutable Git snapshot")
	}
	foundRestricted := false
	for _, change := range snapshot.Changes {
		if change.Path == "private/details.go" {
			foundRestricted = change.Restricted && !change.ContentIncluded
		}
	}
	if !foundRestricted {
		t.Fatalf("restricted change was not classified: %#v", snapshot.Changes)
	}
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var packet contextpack.Packet
	readJSONFile(t, routed.PacketPath, &packet)
	packetJSON := string(marshalJSON(t, packet))
	if strings.Contains(packetJSON, sentinel) || slices.Contains(packet.ChangedFiles, "private/details.go") {
		t.Fatal("project-restricted content/path leaked into packet")
	}
}

func TestGeneratedContractMetadataRoutesWithoutIncludingBody(t *testing.T) {
	repository := newRepository(t)
	if err := os.MkdirAll(filepath.Join(repository, "api"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repository, "api", "service.pb.go"), "package api\n\nconst generatedValue = 1\n")
	gitRun(t, repository, "add", "api/service.pb.go")
	gitRun(t, repository, "commit", "-m", "add generated contract")
	const sentinel = "GENERATED_BODY_SENTINEL_8841"
	writeFile(t, filepath.Join(repository, "api", "service.pb.go"), "package api\n\nconst generatedValue = \""+sentinel+"\"\n")
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree})
	if err != nil {
		t.Fatal(err)
	}
	collected, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID})
	if err != nil {
		t.Fatal(err)
	}
	if !collected.Reviewable {
		t.Fatal("generated contract metadata did not count as a safe review scope")
	}
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	selectedContract := false
	for _, decision := range routed.Routing.Selected {
		selectedContract = selectedContract || decision.Role == "contract-reviewer"
	}
	if !selectedContract {
		t.Fatalf("contract reviewer was not routed: %#v", routed.Routing.Selected)
	}
	var packet contextpack.Packet
	readJSONFile(t, routed.PacketPath, &packet)
	if !slices.Contains(packet.ChangedFiles, "api/service.pb.go") {
		t.Fatalf("generated metadata path missing: %#v", packet.ChangedFiles)
	}
	if strings.Contains(packet.Diff.Full, sentinel) {
		t.Fatalf("generated body leaked into diff: %s", packet.Diff.Full)
	}
	foundCoverage := false
	for _, limit := range packet.Sources.Excluded {
		if limit.Source == "api/service.pb.go" && strings.Contains(limit.Reason, "generated") {
			foundCoverage = true
		}
	}
	if !foundCoverage {
		t.Fatalf("generated exclusion missing from coverage: %#v", packet.Sources.Excluded)
	}
}

func TestUntrackedProjectInstructionsRequireExplicitContentConsent(t *testing.T) {
	repository := newRepository(t)
	const sentinel = "UNTRACKED_INSTRUCTION_SENTINEL_4182"
	writeFile(t, filepath.Join(repository, "AGENTS.md"), sentinel)
	writeFile(t, filepath.Join(repository, "main.go"), "package example\n\nfunc add(left, right int) int { return left - right }\n")
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repository, "AGENTS.md"), "MUTATED_LIVE_INSTRUCTION_SENTINEL")
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	if err == nil {
		t.Fatal("route unexpectedly ignored post-collection untracked staleness")
	}
	writeFile(t, filepath.Join(repository, "AGENTS.md"), sentinel)
	initialized, err = service.Init(ctx, workflow.InitOptions{Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	routed, err = routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	packetBytes, err := os.ReadFile(routed.PacketPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(packetBytes), sentinel) {
		t.Fatal("untracked instruction content leaked without consent")
	}
	var packet contextpack.Packet
	readJSONFile(t, routed.PacketPath, &packet)
	if len(packet.ProjectInstructions) != 0 {
		t.Fatalf("untracked instructions entered packet: %#v", packet.ProjectInstructions)
	}
}

func TestTrackedInstructionSymlinkCannotEscapeRepository(t *testing.T) {
	repository := newRepository(t)
	outside := filepath.Join(t.TempDir(), "outside-instructions")
	const sentinel = "OUTSIDE_SYMLINK_SENTINEL_9291"
	writeFile(t, outside, sentinel)
	if err := os.Symlink(outside, filepath.Join(repository, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repository, "add", "AGENTS.md")
	gitRun(t, repository, "commit", "-m", "track instruction symlink")
	writeFile(t, filepath.Join(repository, "main.go"), "package example\n\nfunc add(left, right int) int { return left - right }\n")
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	packetBytes, err := os.ReadFile(routed.PacketPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(packetBytes), sentinel) {
		t.Fatal("outside symlink content leaked")
	}
}

func TestProjectConfigSymlinkIsRejectedWithoutReadingTarget(t *testing.T) {
	repository := newRepository(t)
	if err := os.MkdirAll(filepath.Join(repository, ".zephyr"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, outside, "version: 1\n# CONFIG_SYMLINK_SENTINEL_7731\n")
	if err := os.Symlink(outside, filepath.Join(repository, ".zephyr", "config.yaml")); err != nil {
		t.Fatal(err)
	}
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initialized, err := service.Init(context.Background(), workflow.InitOptions{Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Collect(context.Background(), workflow.CollectOptions{RunID: initialized.RunID})
	if err == nil || !strings.Contains(err.Error(), "symbolic links are not allowed") {
		t.Fatalf("config symlink error = %v", err)
	}
}

func TestPlanSymlinkIsRejectedBeforeSnapshot(t *testing.T) {
	repository := newRepository(t)
	outside := filepath.Join(t.TempDir(), "outside-review-spec.md")
	writeFile(t, outside, "# PLAN_SYMLINK_SENTINEL_1198\n")
	plan := filepath.Join(repository, "REVIEW_SPEC.md")
	if err := os.Symlink(outside, plan); err != nil {
		t.Fatal(err)
	}
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Init(context.Background(), workflow.InitOptions{
		Repository: repository, Mode: run.ModePlan, Source: run.SourcePlanOnly, PlanPath: plan,
	})
	if err == nil || !strings.Contains(err.Error(), "symbolic links are not allowed") {
		t.Fatalf("plan symlink error = %v", err)
	}
}

func TestSecretPlanPathsAreRejectedBeforeRead(t *testing.T) {
	repository := newRepository(t)
	for _, name := range []string{"CLIENT.P12", ".envfoo"} {
		t.Run(name, func(t *testing.T) {
			plan := filepath.Join(t.TempDir(), name)
			writeFile(t, plan, "SECRET_PLAN_SENTINEL_8841")
			service, err := workflow.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Init(context.Background(), workflow.InitOptions{
				Repository: repository, Mode: run.ModePlan, Source: run.SourcePlanOnly, PlanPath: plan,
			})
			if err == nil || !strings.Contains(err.Error(), "denied by baseline secret policy") {
				t.Fatalf("secret plan %q error = %v", name, err)
			}
		})
	}
}

func TestCommitRangeReportNamesReviewedTargetWhenCheckoutHeadDiffers(t *testing.T) {
	repository := newRepository(t)
	base := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(repository, "range.go"), "package example\n\nfunc ranged() int { return 1 }\n")
	gitRun(t, repository, "add", "range.go")
	gitRun(t, repository, "commit", "-m", "range target")
	target := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(repository, "later.go"), "package example\n\nfunc later() int { return 2 }\n")
	gitRun(t, repository, "add", "later.go")
	gitRun(t, repository, "commit", "-m", "later checkout head")
	checkoutHead := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "HEAD"))

	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rangeSpec := base + ".." + target
	initialized, err := service.Init(ctx, workflow.InitOptions{
		Repository: repository, Mode: run.ModeImplementation, Source: run.SourceCommitRange, CommitRange: rangeSpec,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for index, decision := range routed.Routing.Selected {
		if index == 0 {
			if _, err := service.ValidateCandidates(ctx, workflow.ValidateCandidatesOptions{
				RunID: initialized.RunID,
				Role:  decision.Role,
				Input: marshalJSON(t, schema.CandidateEnvelope{
					Version:  schema.ProtocolVersion,
					RunID:    initialized.RunID,
					Role:     decision.Role,
					Findings: []schema.CandidateFinding{},
				}),
			}); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := service.MarkFailed(ctx, workflow.MarkFailedOptions{
			RunID: initialized.RunID, Stage: "review", Role: decision.Role, Reason: "scope metadata test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.ValidateVerdicts(ctx, workflow.ValidateVerdictsOptions{
		RunID: initialized.RunID,
		Input: marshalJSON(t, schema.EvidenceVerdictEnvelope{Version: schema.ProtocolVersion, RunID: initialized.RunID, Verdicts: []schema.EvidenceVerdict{}}),
	}); err != nil {
		t.Fatal(err)
	}
	aggregated, err := service.Aggregate(ctx, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var review struct {
		Scope struct {
			Head        string `json:"head"`
			TargetSHA   string `json:"target_sha"`
			CommitRange string `json:"commit_range"`
		} `json:"scope"`
	}
	readJSONFile(t, aggregated.ReviewPath, &review)
	if review.Scope.Head != checkoutHead || review.Scope.TargetSHA != target || review.Scope.CommitRange != rangeSpec {
		t.Fatalf("wrong commit-range scope: %#v", review.Scope)
	}
}

func TestParallelCandidateValidationPreservesEveryRole(t *testing.T) {
	repository := newRepository(t)
	writeFile(t, filepath.Join(repository, "main.go"), "package example\n\nfunc add(left, right int) int { return left - right }\n")
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errorsByRole := make(chan error, len(routed.Routing.Selected))
	for _, decision := range routed.Routing.Selected {
		role := decision.Role
		wait.Add(1)
		go func() {
			defer wait.Done()
			worker, newErr := workflow.New(service.StoreRoot())
			if newErr != nil {
				errorsByRole <- newErr
				return
			}
			_, validateErr := worker.ValidateCandidates(ctx, workflow.ValidateCandidatesOptions{
				RunID: initialized.RunID, Role: role,
				Input: marshalJSON(t, schema.CandidateEnvelope{Version: schema.ProtocolVersion, RunID: initialized.RunID, Role: role, Findings: []schema.CandidateFinding{}}),
			})
			if validateErr != nil {
				errorsByRole <- validateErr
			}
		}()
	}
	wait.Wait()
	close(errorsByRole)
	for validateErr := range errorsByRole {
		assert.NoError(t, validateErr)
	}
	if t.Failed() {
		return
	}
	var candidates struct {
		RunID    string                    `json:"run_id"`
		Findings []schema.CandidateFinding `json:"findings"`
	}
	readJSONFile(t, filepath.Join(initialized.RunDir, "evidence", "prechecked.json"), &candidates)
	if candidates.RunID != initialized.RunID || candidates.Findings == nil {
		t.Fatalf("merged candidates were lost or malformed: %#v", candidates)
	}
	inspected, err := service.Inspect(ctx, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	reviewComplete := false
	for _, stage := range inspected.Stages {
		if stage.Name == "review" && stage.State == run.StageComplete {
			reviewComplete = true
		}
	}
	if !reviewComplete {
		t.Fatalf("review stage did not account every role: %#v", inspected.Stages)
	}
}

func TestSemanticRoutingRemovesContextOnlyFalsePositivesBeforeReview(t *testing.T) {
	repository := newRepository(t)
	planPath := filepath.Join(repository, "REVIEW_SPEC.md")
	writeFile(t, planPath, "# Plan\n\nUpdate the policy contract and skill authoring workflow.\n")
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{
		Repository: repository, Mode: run.ModePlan, Source: run.SourcePlanOnly, PlanPath: planPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	for _, capability := range []workflow.CapabilitySetOptions{
		{RunID: initialized.RunID, Source: workflow.CapabilityJira, Status: workflow.CapabilityNotRequired, Reason: "no Jira reference"},
		{RunID: initialized.RunID, Source: workflow.CapabilityConfluence, Status: workflow.CapabilityNotRequired, Reason: "no Confluence reference"},
		{RunID: initialized.RunID, Source: workflow.CapabilityBitbucket, Status: workflow.CapabilityAvailable},
	} {
		if _, err := service.SetCapability(ctx, capability); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.AddContext(ctx, workflow.ContextAddOptions{
		RunID: initialized.RunID, Source: "bitbucket", Key: "master",
		Content: []byte("Repository inventory: platform/techstack/react and a migration instruction."),
	}); err != nil {
		t.Fatal(err)
	}
	routed, err := service.Route(ctx, workflow.RouteOptions{RunID: initialized.RunID})
	if err != nil {
		t.Fatal(err)
	}
	if routed.RoutingPath != "" {
		t.Fatal("routing unexpectedly finalized before semantic decision")
	}
	startedTrace := semanticTraceEvent(t, initialized.RunDir)
	if startedTrace.Status != trace.StatusStarted || startedTrace.FinishedAt != nil {
		t.Fatalf("semantic routing trace did not start before model dispatch: %#v", startedTrace)
	}
	if _, err := service.ValidateCandidates(ctx, workflow.ValidateCandidatesOptions{
		RunID: initialized.RunID, Role: "architect-reviewer",
		Input: marshalJSON(t, schema.CandidateEnvelope{Version: 1, RunID: initialized.RunID, Role: "architect-reviewer", Findings: []schema.CandidateFinding{}}),
	}); err == nil || !strings.Contains(err.Error(), "stage \"route\" is \"running\"") {
		t.Fatalf("review unexpectedly started before semantic routing: %v", err)
	}
	proposal := schema.SemanticRoutingEnvelope{Version: routing.SemanticRoutingVersion, RunID: initialized.RunID, Decisions: []schema.SemanticRoutingDecision{}}
	for _, candidate := range routed.RoutingRequest.Candidates {
		decision := "exclude"
		if candidate.Role == "contract-reviewer" || candidate.Role == "skill-authoring-expert" {
			decision = "include"
		}
		proposal.Decisions = append(proposal.Decisions, schema.SemanticRoutingDecision{
			Role: candidate.Role, Decision: decision, EvidenceRefs: []string{"scope"}, Reason: "fixture scope classification", Confidence: 0.9,
		})
	}
	finalized, err := service.ValidateRouting(ctx, workflow.ValidateRoutingOptions{RunID: initialized.RunID, Input: marshalJSON(t, proposal)})
	if err != nil {
		t.Fatal(err)
	}
	if finalized.Routing.Degraded || decisionPresent(finalized.Routing.Selected, "frontend-expert") || decisionPresent(finalized.Routing.Selected, "sql-expert") {
		t.Fatalf("context-only false positives survived semantic routing: %#v", finalized.Routing)
	}
	for _, role := range []string{"architect-reviewer", "contract-reviewer", "skill-authoring-expert"} {
		if !decisionPresent(finalized.Routing.Selected, role) {
			t.Fatalf("expected role %q missing", role)
		}
	}
	finishedTrace := semanticTraceEvent(t, initialized.RunDir)
	if finishedTrace.Status != trace.StatusCompleted || finishedTrace.Metadata["validation_status"] != "accepted" ||
		finishedTrace.Metadata["fallback"] != "false" || finishedTrace.Metadata["selected_roles"] != "3" {
		t.Fatalf("semantic routing trace lacks final lifecycle metadata: %#v", finishedTrace)
	}
}

func TestInvalidSemanticRoutingUsesConservativeFallback(t *testing.T) {
	repository := newRepository(t)
	planPath := filepath.Join(repository, "REVIEW_SPEC.md")
	writeFile(t, planPath, "# Plan\n\nReview the change.\n")
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{Repository: repository, Mode: run.ModePlan, Source: run.SourcePlanOnly, PlanPath: planPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	for _, source := range []workflow.CapabilitySource{workflow.CapabilityJira, workflow.CapabilityConfluence, workflow.CapabilityBitbucket} {
		if _, err := service.SetCapability(ctx, workflow.CapabilitySetOptions{RunID: initialized.RunID, Source: source, Status: workflow.CapabilityNotRequired, Reason: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Route(ctx, workflow.RouteOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	finalized, err := service.ValidateRouting(ctx, workflow.ValidateRoutingOptions{RunID: initialized.RunID, Input: []byte(`{"invalid":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !finalized.Routing.Degraded || finalized.Routing.Resolution != "deterministic-fallback" || len(finalized.Routing.Selected) != len(config.KnownRoles()) {
		t.Fatalf("invalid semantic output did not preserve coverage: %#v", finalized.Routing)
	}
	fallbackTrace := semanticTraceEvent(t, initialized.RunDir)
	wantSelectedRoles := fmt.Sprint(len(config.KnownRoles()))
	if fallbackTrace.Status != trace.StatusPartial || fallbackTrace.Metadata["validation_status"] != "invalid" ||
		fallbackTrace.Metadata["fallback_category"] != "invalid-output" || fallbackTrace.Metadata["selected_roles"] != wantSelectedRoles {
		t.Fatalf("semantic fallback trace lacks final lifecycle metadata: %#v", fallbackTrace)
	}
}

func TestSemanticRoutingTimeoutUsesConservativeFallbackBeforeReview(t *testing.T) {
	repository := newRepository(t)
	planPath := filepath.Join(repository, "REVIEW_SPEC.md")
	writeFile(t, planPath, "# Plan\n\nReview the change.\n")
	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{Repository: repository, Mode: run.ModePlan, Source: run.SourcePlanOnly, PlanPath: planPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	for _, source := range []workflow.CapabilitySource{workflow.CapabilityJira, workflow.CapabilityConfluence, workflow.CapabilityBitbucket} {
		if _, err := service.SetCapability(ctx, workflow.CapabilitySetOptions{RunID: initialized.RunID, Source: source, Status: workflow.CapabilityNotRequired, Reason: "fixture"}); err != nil {
			t.Fatal(err)
		}
	}
	routed, err := service.Route(ctx, workflow.RouteOptions{RunID: initialized.RunID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateCandidates(ctx, workflow.ValidateCandidatesOptions{
		RunID: initialized.RunID, Role: config.RoleArchitectReviewer,
		Input: marshalJSON(t, schema.CandidateEnvelope{Version: 1, RunID: initialized.RunID, Role: config.RoleArchitectReviewer, Findings: []schema.CandidateFinding{}}),
	}); err == nil {
		t.Fatal("reviewer started before semantic timeout fallback completed")
	}
	finalized, err := service.FallbackRouting(ctx, workflow.FinalizeRoutingOptions{RunID: initialized.RunID, Reason: "semantic router timeout"})
	if err != nil {
		t.Fatal(err)
	}
	if !finalized.Routing.Degraded || len(finalized.Routing.Selected) != len(config.KnownRoles()) || routed.RoutingPath != "" {
		t.Fatalf("timeout fallback did not preserve coverage: %#v", finalized.Routing)
	}
	fallbackTrace := semanticTraceEvent(t, initialized.RunDir)
	wantSelectedRoles := fmt.Sprint(len(config.KnownRoles()))
	if fallbackTrace.Status != trace.StatusPartial || fallbackTrace.Metadata["fallback_category"] != "timeout" ||
		fallbackTrace.Metadata["selected_roles"] != wantSelectedRoles {
		t.Fatalf("timeout fallback trace is incomplete: %#v", fallbackTrace)
	}
}

func semanticTraceEvent(t *testing.T, runDir string) trace.Event {
	t.Helper()
	var value trace.Trace
	readJSONFile(t, filepath.Join(runDir, "trace.json"), &value)
	var found *trace.Event
	for i := range value.Events {
		if value.Events[i].Stage != "semantic-routing" {
			continue
		}
		if found != nil {
			t.Fatal("multiple semantic-routing trace events")
		}
		found = &value.Events[i]
	}
	if found == nil {
		t.Fatal("semantic-routing trace event is missing")
	}
	return *found
}

func decisionPresent(decisions []routing.Decision, role string) bool {
	for _, decision := range decisions {
		if decision.Role == role {
			return true
		}
	}
	return false
}

func TestTrackedCredentialAssignmentsAreRedactedFromPacket(t *testing.T) {
	repository := newRepository(t)
	const (
		awsSecret  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
		kubeSecret = "LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0t"
	)
	writeFile(t, filepath.Join(repository, "main.go"), strings.Join([]string{
		"package example",
		"",
		`const aws = "AWS_SECRET_ACCESS_KEY=` + awsSecret + `"`,
		`const kube = "client-key-data: ` + kubeSecret + `"`,
	}, "\n")+"\n")

	service, err := workflow.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{
		Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID}); err != nil {
		t.Fatal(err)
	}
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := os.ReadFile(routed.PacketPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{awsSecret, kubeSecret} {
		if strings.Contains(string(packet), secret) {
			t.Fatal("tracked credential leaked into packet")
		}
	}
	if !strings.Contains(string(packet), "[REDACTED]") {
		t.Fatal("packet does not record credential redaction")
	}
}

func TestPrepareEvidenceWritesMinimalStableArtifactAfterReview(t *testing.T) {
	repository, service, runID := prepareEvidenceReadyRun(t)
	ctx := context.Background()

	beforeReview, err := workflow.New(t.TempDir())
	require.NoError(t, err)
	beforeReviewRun, err := beforeReview.Init(ctx, workflow.InitOptions{Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree})
	require.NoError(t, err)
	require.NoError(t, func() error {
		_, err := beforeReview.Collect(ctx, workflow.CollectOptions{RunID: beforeReviewRun.RunID})
		return err
	}())
	_, err = beforeReview.PrepareEvidence(ctx, workflow.PrepareEvidenceOptions{RunID: beforeReviewRun.RunID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stage \"route\"")

	first, err := service.PrepareEvidence(ctx, workflow.PrepareEvidenceOptions{RunID: runID})
	require.NoError(t, err)
	assert.Equal(t, runID, first.RunID)
	assert.Equal(t, 1, first.Items)
	firstBytes, err := os.ReadFile(first.Evidence)
	require.NoError(t, err)
	assert.NotContains(t, string(firstBytes), repository)
	assert.NotContains(t, string(firstBytes), "review-packet")
	assert.NotContains(t, string(firstBytes), "func unrelatedChanged() {}")
	assert.Contains(t, string(firstBytes), "func changed() {}")

	before, err := service.Inspect(ctx, runID)
	require.NoError(t, err)
	second, err := service.PrepareEvidence(ctx, workflow.PrepareEvidenceOptions{RunID: runID})
	require.NoError(t, err)
	secondBytes, err := os.ReadFile(second.Evidence)
	require.NoError(t, err)
	after, err := service.Inspect(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, firstBytes, secondBytes)
	assert.Equal(t, before.Stages, after.Stages)
	assert.Equal(t, first.Evidence, after.Artifacts.MinimalEvidence)
}

func TestPrepareEvidenceRejectsMissingValidatedReviewerAndFrozenMismatch(t *testing.T) {
	repository := newRepository(t)
	writeFile(t, filepath.Join(repository, "main.go"), "package example\n\nfunc changed() {}\n")
	service, err := workflow.New(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree})
	require.NoError(t, err)
	require.NoError(t, func() error {
		_, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID})
		return err
	}())
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	require.NoError(t, err)
	for _, decision := range routed.Routing.Selected {
		require.NoError(t, func() error {
			_, err := service.MarkFailed(ctx, workflow.MarkFailedOptions{RunID: initialized.RunID, Stage: "review", Role: decision.Role, Reason: "fixture failure"})
			return err
		}())
	}
	_, err = service.PrepareEvidence(ctx, workflow.PrepareEvidenceOptions{RunID: initialized.RunID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no selected reviewer produced a validated result")

	_, readyService, readyRunID := prepareEvidenceReadyRun(t)
	inspected, err := readyService.Inspect(ctx, readyRunID)
	require.NoError(t, err)
	require.NotEmpty(t, inspected.Artifacts.Candidates)
	require.NoError(t, os.WriteFile(inspected.Artifacts.Candidates, []byte(`{"version":1,"run_id":"other","findings":[]}`), 0o600))
	_, err = readyService.PrepareEvidence(ctx, workflow.PrepareEvidenceOptions{RunID: readyRunID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must match run")
}

func TestPrepareEvidenceRejectsStaleWorkingTree(t *testing.T) {
	repository, service, runID := prepareEvidenceReadyRun(t)
	writeFile(t, filepath.Join(repository, "main.go"), "package example\n\nfunc changedAgain() {}\n")
	_, err := service.PrepareEvidence(context.Background(), workflow.PrepareEvidenceOptions{RunID: runID})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Git input changed")
}

func prepareEvidenceReadyRun(t *testing.T) (string, *workflow.Service, string) {
	t.Helper()
	repository := newRepository(t)
	writeFile(t, filepath.Join(repository, "unrelated.go"), "package example\n\nfunc unrelated() {}\n")
	gitRun(t, repository, "add", "unrelated.go")
	gitRun(t, repository, "commit", "-m", "add unrelated fixture")
	writeFile(t, filepath.Join(repository, "main.go"), "package example\n\nfunc changed() {}\n")
	writeFile(t, filepath.Join(repository, "unrelated.go"), "package example\n\nfunc unrelatedChanged() {}\n")
	service, err := workflow.New(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	initialized, err := service.Init(ctx, workflow.InitOptions{Repository: repository, Mode: run.ModeImplementation, Source: run.SourceWorkingTree})
	require.NoError(t, err)
	require.NoError(t, func() error {
		_, err := service.Collect(ctx, workflow.CollectOptions{RunID: initialized.RunID})
		return err
	}())
	routed, err := routeWithNoExternalContext(t, ctx, service, initialized.RunID)
	require.NoError(t, err)
	for _, decision := range routed.Routing.Selected {
		envelope := schema.CandidateEnvelope{Version: schema.ProtocolVersion, RunID: initialized.RunID, Role: decision.Role, Findings: []schema.CandidateFinding{}}
		if decision.Role == config.RoleCodeReviewer {
			code := "func changed() {}"
			envelope.Findings = []schema.CandidateFinding{{
				ID:       "code-reviewer-prepare-evidence",
				Role:     decision.Role,
				Severity: schema.SeverityP1,
				Category: "functional-correctness",
				Title:    "Changed implementation requires evidence",
				Location: schema.FindingLocation{File: "main.go", LineStart: 3, LineEnd: 3},
				Evidence: schema.FindingEvidence{
					Code:              &code,
					ExecutionPath:     "caller reaches the changed function",
					ViolatedInvariant: "changed behavior remains intentional",
					FalsifierChecked:  "no compensating caller exists",
				},
				Impact:         "caller behavior can regress",
				Recommendation: "confirm the frozen changed line",
				Confidence:     0.9,
			}}
		}
		_, err := service.ValidateCandidates(ctx, workflow.ValidateCandidatesOptions{RunID: initialized.RunID, Role: decision.Role, Input: marshalJSON(t, envelope)})
		require.NoError(t, err)
	}
	return repository, service, initialized.RunID
}

func newRepository(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	repository := filepath.Join(t.TempDir(), "secret-project")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repository, "init", "-b", "main")
	gitRun(t, repository, "config", "user.name", "Zephyr Test")
	gitRun(t, repository, "config", "user.email", "zephyr@example.invalid")
	writeFile(t, filepath.Join(repository, "main.go"), "package example\n\nfunc add(left, right int) int { return left + right }\n")
	gitRun(t, repository, "add", "main.go")
	gitRun(t, repository, "commit", "-m", "initial")
	return repository
}

func routeWithNoExternalContext(
	t *testing.T,
	ctx context.Context,
	service *workflow.Service,
	runID string,
) (workflow.RouteResult, error) {
	t.Helper()
	for _, source := range []workflow.CapabilitySource{
		workflow.CapabilityJira,
		workflow.CapabilityConfluence,
		workflow.CapabilityBitbucket,
	} {
		if _, err := service.SetCapability(ctx, workflow.CapabilitySetOptions{
			RunID: runID, Source: source, Status: workflow.CapabilityNotRequired,
			Reason: "fixture has no external requirement reference",
		}); err != nil {
			t.Fatalf("record %s capability: %v", source, err)
		}
	}
	routed, err := service.Route(ctx, workflow.RouteOptions{RunID: runID})
	if err != nil {
		return workflow.RouteResult{}, err
	}
	if routed.RoutingPath != "" {
		return routed, nil
	}
	finalized, err := service.FallbackRouting(ctx, workflow.FinalizeRoutingOptions{
		RunID: runID, Reason: "workflow fixture uses deterministic fallback",
	})
	if err != nil {
		return workflow.RouteResult{}, err
	}
	routed.Routing = finalized.Routing
	routed.RoutingPath = finalized.RoutingPath
	return routed, nil
}

func gitRun(t *testing.T, repository string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func marshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readJSONFile(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
