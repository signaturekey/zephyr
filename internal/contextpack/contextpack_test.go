package contextpack

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signaturekey/zephyr/internal/redaction"
)

func TestBuildCollectsAndTruncatesContext(t *testing.T) {
	repo := t.TempDir()
	runDir := t.TempDir()
	mustMkdir(t, filepath.Join(repo, "internal"))
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), "root instructions")
	mustWrite(t, filepath.Join(repo, "internal", "AGENTS.md"), "nested instructions")
	planPath := filepath.Join(repo, "REVIEW_SPEC.md")
	mustWrite(t, planPath, "implementation plan")
	mustMkdir(t, filepath.Join(runDir, "git"))
	mustWrite(t, filepath.Join(runDir, "git", "metadata.json"), `{"head":"abc"}`)
	mustWrite(t, filepath.Join(runDir, "git", "diff.patch"), strings.Repeat("x", 100))
	instructions := SnapshotInstructions(repo, []string{"AGENTS.md", "internal/AGENTS.md"}, 1024, redaction.DefaultPolicy(nil))

	result, err := Build(Options{
		RunDir:           runDir,
		RunID:            "run-1",
		Mode:             "alignment",
		Source:           "working-tree",
		RepoRoot:         repo,
		Repository:       Repository{Root: repo, Head: "abc"},
		ChangedFiles:     []string{"internal/service.go"},
		PlanPath:         planPath,
		MaxDiffBytes:     20,
		MaxDocumentBytes: 1024,
		Redaction:        redaction.DefaultPolicy(nil),
		Instructions:     instructions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Packet.Diff.Truncated || len(result.Truncations) != 1 {
		t.Fatalf("expected one diff truncation, got %#v", result.Truncations)
	}
	if result.Packet.Plan == nil || result.Packet.Plan.Content != "implementation plan" {
		t.Fatalf("unexpected plan: %#v", result.Packet.Plan)
	}
	if len(result.Packet.ProjectInstructions) != 2 {
		t.Fatalf("instructions = %d, want 2", len(result.Packet.ProjectInstructions))
	}
	if got := result.Packet.Technologies; len(got) != 1 || got[0] != "go" {
		t.Fatalf("technologies = %v", got)
	}
	if len(result.Packet.RoutingSignals) == 0 || result.Packet.RoutingSignals[0] != "architecture" {
		t.Fatalf("routing signals = %v", result.Packet.RoutingSignals)
	}
	if len(result.Packet.CoverageLimits) != 1 {
		t.Fatalf("coverage limits = %v", result.Packet.CoverageLimits)
	}
	packetBytes, err := json.Marshal(result.Packet)
	if err != nil {
		t.Fatal(err)
	}
	for _, privatePath := range []string{repo, runDir} {
		if strings.Contains(string(packetBytes), privatePath) {
			t.Fatalf("reviewer packet leaked absolute path %q: %s", privatePath, packetBytes)
		}
	}
	if result.Packet.Repository.Root != "reviewed-repository" {
		t.Fatalf("reviewer repository root = %q", result.Packet.Repository.Root)
	}
}

func TestSnapshotInstructionsUsesBoundedPrefixHash(t *testing.T) {
	repo := t.TempDir()
	const limit = int64(32)
	prefix := strings.Repeat("a", int(limit))
	mustWrite(t, filepath.Join(repo, "AGENTS.md"), prefix+strings.Repeat("tail", 1024))

	snapshot := SnapshotInstructions(repo, []string{"AGENTS.md"}, limit, redaction.DefaultPolicy(nil))
	if len(snapshot.Documents) != 1 || !snapshot.Documents[0].Truncated || len(snapshot.Truncations) != 1 {
		t.Fatalf("unexpected bounded instruction snapshot: %#v", snapshot)
	}
	digest := sha256.Sum256([]byte(prefix))
	wantHash := "sha256:" + fmt.Sprintf("%x", digest)
	if snapshot.Documents[0].ContentHash != wantHash {
		t.Fatalf("content hash = %q, want bounded prefix hash %q", snapshot.Documents[0].ContentHash, wantHash)
	}
}

func TestPlanContentProducesBoundedSemanticRoutingSignals(t *testing.T) {
	runDir := t.TempDir()
	plan := filepath.Join(t.TempDir(), "REVIEW_SPEC.md")
	mustWrite(t, plan, "Add authorization roles, an online SQL migration, and a backward-compatible OpenAPI contract. Acceptance criteria require a negative test.")
	result, err := Build(Options{
		RunDir: runDir, RunID: "run", Mode: "plan", Source: "plan-only", PlanPath: plan,
		Redaction: redaction.DefaultPolicy(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"architecture", "contract", "security", "sql", "tests"} {
		if !containsString(result.Packet.RoutingSignals, expected) {
			t.Fatalf("signal %q missing from %v", expected, result.Packet.RoutingSignals)
		}
	}
	for _, weak := range []string{"contract", "security", "sql", "tests"} {
		if containsString(result.Packet.StrongRoutingSignals, weak) {
			t.Fatalf("plan-only signal %q was incorrectly protected: %v", weak, result.Packet.StrongRoutingSignals)
		}
	}
}

func TestFrontendAndSkillPathsProduceTechnologyAndRoutingSignals(t *testing.T) {
	paths := []string{
		"src/pages/profile.tsx",
		"src/api/profile.ts",
		"src/styles.module.scss",
		"frontend/skills/example/SKILL.md",
		"services/payments/AGENTS.md",
		"frontend/CLAUDE.md",
	}
	technologies := detectTechnologies(paths)
	for _, expected := range []string{"codex-skill", "css", "frontend", "markdown", "typescript"} {
		if !containsString(technologies, expected) {
			t.Fatalf("technology %q missing from %v", expected, technologies)
		}
	}
	packet := Packet{Mode: "implementation", ChangedFiles: paths}
	for _, expected := range []string{"frontend", "observable-behavior", "skill-authoring", "typescript"} {
		if signals := detectRoutingSignals(packet); !containsString(signals, expected) {
			t.Fatalf("signal %q missing from %v", expected, signals)
		}
	}
	for _, expected := range []string{"frontend", "skill-authoring", "typescript"} {
		if signals := detectStrongRoutingSignals(packet); !containsString(signals, expected) {
			t.Fatalf("strong path signal %q missing from %v", expected, signals)
		}
	}
}

func TestPythonPathsProduceTechnologyAndRoutingSignals(t *testing.T) {
	paths := []string{"services/payments/worker.py", "pyproject.toml", "uv.lock"}
	technologies := detectTechnologies(paths)
	if !containsString(technologies, "python") {
		t.Fatalf("python technology missing from %v", technologies)
	}

	packet := Packet{Mode: "implementation", ChangedFiles: paths}
	for _, expected := range []string{"python", "observable-behavior"} {
		if signals := detectRoutingSignals(packet); !containsString(signals, expected) {
			t.Fatalf("signal %q missing from %v", expected, signals)
		}
		if signals := detectStrongRoutingSignals(packet); !containsString(signals, expected) {
			t.Fatalf("strong signal %q missing from %v", expected, signals)
		}
	}
}

func TestOperationalPathsProduceRoutingSignals(t *testing.T) {
	packet := Packet{
		Mode: "implementation",
		ChangedFiles: []string{
			"internal/resilience/retry.go",
			"internal/databus/consumer.go",
			"deploy/k8s/deployment.yaml",
			"internal/cache/redis.go",
		},
	}
	signals := detectRoutingSignals(packet)
	for _, expected := range []string{"infrastructure", "messaging", "reliability", "storage"} {
		if !containsString(signals, expected) {
			t.Fatalf("signal %q missing from %v", expected, signals)
		}
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestBuildValidatesBusinessSnapshotHash(t *testing.T) {
	runDir := t.TempDir()
	mustMkdir(t, filepath.Join(runDir, "context", "jira"))
	snapshot := BusinessSnapshot{
		Source:      "jira",
		Key:         "RINT-1",
		FetchedAt:   time.Now().UTC(),
		ContentHash: "sha256:not-the-hash",
		Content:     "requirements",
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(runDir, "context", "jira", "RINT-1.json"), string(data))

	result, err := Build(Options{RunDir: runDir, RunID: "run", Mode: "plan", Source: "plan-only", Redaction: redaction.DefaultPolicy(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Packet.BusinessContext) != 0 || len(result.Packet.Sources.Excluded) != 1 {
		t.Fatalf("bad snapshot must be excluded: %#v", result.Packet.Sources)
	}
}

func TestBuildRedactsDiff(t *testing.T) {
	runDir := t.TempDir()
	mustMkdir(t, filepath.Join(runDir, "git"))
	mustWrite(t, filepath.Join(runDir, "git", "diff.patch"), "+password=hunter2\n")

	result, err := Build(Options{RunDir: runDir, RunID: "run", Mode: "implementation", Source: "working-tree", Redaction: redaction.DefaultPolicy(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Packet.Diff.Full, "hunter2") {
		t.Fatalf("secret leaked into packet: %s", result.Packet.Diff.Full)
	}
}

func TestBuildRedactsPrivateKeyWhenDiffTruncatesBeforeEndMarker(t *testing.T) {
	runDir := t.TempDir()
	mustMkdir(t, filepath.Join(runDir, "git"))
	mustWrite(t, filepath.Join(runDir, "git", "diff.patch"), "+-----BEGIN PRIVATE KEY-----\n+PARTIAL_PRIVATE_KEY_SENTINEL\n+more\n+-----END PRIVATE KEY-----\n")

	result, err := Build(Options{
		RunDir: runDir, RunID: "run", Mode: "implementation", Source: "working-tree",
		MaxDiffBytes: 70, Redaction: redaction.DefaultPolicy(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Packet.Diff.Full, "PARTIAL_PRIVATE_KEY_SENTINEL") || strings.Contains(result.Packet.Diff.Full, "BEGIN PRIVATE KEY") {
		t.Fatalf("truncated private key leaked into packet: %q", result.Packet.Diff.Full)
	}
}

func TestBuildDoesNotDuplicateFullDiffWithIndexDiffs(t *testing.T) {
	runDir := t.TempDir()
	mustMkdir(t, filepath.Join(runDir, "git"))
	mustWrite(t, filepath.Join(runDir, "git", "diff.patch"), "full")
	mustWrite(t, filepath.Join(runDir, "git", "staged.patch"), "staged")
	mustWrite(t, filepath.Join(runDir, "git", "unstaged.patch"), "unstaged")

	result, err := Build(Options{RunDir: runDir, RunID: "run", Mode: "implementation", Source: "working-tree", Redaction: redaction.DefaultPolicy(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Packet.Diff.Full != "full" || result.Packet.Diff.Staged != "" || result.Packet.Diff.Unstaged != "" {
		t.Fatalf("review packet duplicated diff content: %#v", result.Packet.Diff)
	}
}

func TestSaveBusinessSnapshot(t *testing.T) {
	runDir := t.TempDir()
	path, snapshot, err := SaveBusinessSnapshot(runDir, BusinessSnapshotInput{
		Source:    "JIRA",
		Key:       "RINT/123",
		URL:       "https://jira.example/browse/RINT-123",
		FetchedAt: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
		Content:   "Acceptance criteria",
	}, redaction.DefaultPolicy(nil))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "RINT-123.json" || snapshot.Source != "jira" || !strings.HasPrefix(snapshot.ContentHash, "sha256:") {
		t.Fatalf("unexpected snapshot: path=%s snapshot=%#v", path, snapshot)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	bitbucketPath, bitbucket, err := SaveBusinessSnapshot(runDir, BusinessSnapshotInput{
		Source: "BITBUCKET", Key: "AGENT/hr-tech-dev-skills#51", Content: "PR metadata and review comments",
	}, redaction.DefaultPolicy(nil))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(bitbucketPath) != "AGENT-hr-tech-dev-skills-51.json" || bitbucket.Source != "bitbucket" {
		t.Fatalf("unexpected Bitbucket snapshot: path=%s snapshot=%#v", bitbucketPath, bitbucket)
	}
}

func TestSaveBusinessSnapshotRedactsURLCredentials(t *testing.T) {
	runDir := t.TempDir()
	_, snapshot, err := SaveBusinessSnapshot(runDir, BusinessSnapshotInput{
		Source:  "jira",
		Key:     "RINT-124",
		URL:     "https://user:password@example.invalid/path?access_token=live-token",
		Content: "requirements",
	}, redaction.DefaultPolicy(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"password", "live-token"} {
		if strings.Contains(snapshot.URL, secret) {
			t.Fatalf("URL secret %q leaked in %q", secret, snapshot.URL)
		}
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
