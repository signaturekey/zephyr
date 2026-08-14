package review

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signaturekey/zephyr/internal/codexharness/preflight"
	"github.com/signaturekey/zephyr/internal/codexharness/process"
	"github.com/stretchr/testify/require"
)

func TestDriverIntegration(t *testing.T) {
	fixture := newDriverFixture(t)

	t.Run("success", func(t *testing.T) {
		before := fixture.gitState(t)
		result, stderr, exit := fixture.run(t, "")
		require.Zero(t, exit, stderr)
		require.Equal(t, "complete", result.Status)
		require.NotEmpty(t, result.RunID)
		require.FileExists(t, result.ReviewJSON)
		require.FileExists(t, result.ReviewMarkdown)
		require.Equal(t, before, fixture.gitState(t), "driver must not mutate the reviewed repository")
	})

	t.Run("evidence failure is incomplete and skips aggregation", func(t *testing.T) {
		result, stderr, exit := fixture.run(t, "evidence-fail")
		require.Equal(t, 1, exit, stderr)
		require.Equal(t, "incomplete", result.Status)
		require.Equal(t, string(StageEvidence), result.FailedStage)
		require.Empty(t, result.ReviewJSON)
		require.Empty(t, result.ReviewMarkdown)
	})
}

type driverFixture struct {
	root, bin, repository, driverRoot, core, driver, codex, evidenceCodex, dispatcher string
}

func newDriverFixture(t *testing.T) driverFixture {
	t.Helper()
	root := repositoryRoot(t)
	work := t.TempDir()
	bin := filepath.Join(work, "bin")
	require.NoError(t, os.Mkdir(bin, 0o700))
	core := filepath.Join(bin, "zephyr")
	driver := filepath.Join(bin, "zephyr-codex")
	build(t, root, core, "./cmd/zephyr")
	build(t, root, driver, "./cmd/zephyr-codex")

	repository := filepath.Join(work, "reviewed")
	require.NoError(t, os.Mkdir(repository, 0o700))
	git(t, repository, "init")
	git(t, repository, "config", "user.email", "zephyr@example.test")
	git(t, repository, "config", "user.name", "Zephyr Test")
	require.NoError(t, os.WriteFile(filepath.Join(repository, "main.go"), []byte("package fixture\n\nfunc Value() int { return 1 }\n"), 0o600))
	git(t, repository, "add", "main.go")
	git(t, repository, "commit", "-m", "initial")
	require.NoError(t, os.WriteFile(filepath.Join(repository, "main.go"), []byte("package fixture\n\nfunc Value() int { return 2 }\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "preexisting-untracked.txt"), []byte("keep\n"), 0o600))

	codex := filepath.Join(bin, "codex")
	writeFakeCodex(t, codex, false)
	evidenceCodex := filepath.Join(bin, "codex-evidence-fail")
	writeFakeCodex(t, evidenceCodex, true)
	_, err := preflight.Check(context.Background(), preflight.Options{ZephyrPath: core, CodexPath: codex, DispatcherPath: filepath.Join(root, "harnesses", "codex", "dispatch.sh"), Home: work, CodexHome: filepath.Join(work, "codex-home"), Runner: process.ExecRunner{}, CoreEnv: []string{"PATH=" + os.Getenv("PATH"), "LANG=C", "LC_ALL=C", "TMPDIR=" + work, "ZEPHYR_RUN_ROOT=" + filepath.Join(work, "runs")}})
	require.NoError(t, err)
	return driverFixture{
		root: root, bin: bin, repository: repository, driverRoot: filepath.Join(work, "driver"),
		core: core, driver: driver, codex: codex, evidenceCodex: evidenceCodex, dispatcher: filepath.Join(root, "harnesses", "codex", "dispatch.sh"),
	}
}

func (fixture driverFixture) run(t *testing.T, mode string) (Result, string, int) {
	t.Helper()
	codex := fixture.codex
	if mode == "evidence-fail" {
		codex = fixture.evidenceCodex
	}
	command := exec.Command(fixture.driver, "review", "--repo", fixture.repository)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Dir(fixture.driverRoot),
		"TMPDIR=" + filepath.Dir(fixture.driverRoot),
		"ZEPHYR_CORE_BIN=" + fixture.core,
		"ZEPHYR_CODEX_BIN=" + codex,
		"ZEPHYR_CODEX_DISPATCHER=" + fixture.dispatcher,
		"ZEPHYR_CODEX_DRIVER_ROOT=" + fixture.driverRoot,
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	exit := 0
	if err != nil {
		var processError *exec.ExitError
		require.ErrorAs(t, err, &processError)
		exit = processError.ExitCode()
	}
	var result Result
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result), "stdout=%s stderr=%s", stdout.String(), stderr.String())
	t.Logf("driver result=%+v stderr=%q", result, stderr.String())
	return result, stderr.String(), exit
}

func (fixture driverFixture) gitState(t *testing.T) string {
	t.Helper()
	head := git(t, fixture.repository, "rev-parse", "HEAD")
	status := git(t, fixture.repository, "status", "--porcelain=v1")
	index := git(t, fixture.repository, "write-tree")
	return head + "\n" + status + "\n" + index
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(directory, "..", "..", ".."))
}

func build(t *testing.T, root, output, packagePath string) {
	t.Helper()
	command := exec.Command("go", "build", "-trimpath", "-o", output, packagePath)
	command.Dir = root
	outputBytes, err := command.CombinedOutput()
	require.NoError(t, err, string(outputBytes))
}

func git(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	return strings.TrimSpace(string(output))
}

func writeFakeCodex(t *testing.T, path string, evidenceFailure bool) {
	t.Helper()
	script := `#!/bin/sh
set -eu
if [ "$1" = "--version" ]; then echo codex-0.0.0; exit 0; fi
if [ "$1" = "login" ]; then exit 0; fi
if [ "$1" = "features" ]; then exit 0; fi
if [ "$1" = "exec" ] && [ "${2:-}" = "--help" ]; then
  echo '--strict-config --ignore-user-config --ignore-rules --ephemeral --sandbox --output-schema --output-last-message --json'
  exit 0
fi
[ "$1" = "exec" ] || exit 2
schema= output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output-schema) schema=$2; shift 2 ;;
    --output-last-message) output=$2; shift 2 ;;
    *) shift ;;
  esac
done
prompt=$(mktemp)
trap 'rm -f "$prompt"' EXIT
cat > "$prompt"
run_id_from_block() {
  label=$1
  awk -v label="$label" '
    index($0, " open label=" label " ") { inside=1; next }
    inside && index($0, " close label=" label "]") { exit }
    inside && /"run_id":[[:space:]]*"/ {
      line=$0
      sub(/.*"run_id":[[:space:]]*"/, "", line)
      sub(/".*/, "", line)
      print line
      exit
    }
  ' "$prompt"
}
case "$schema" in
  *semantic-routing*)
    run_id=$(run_id_from_block review-packet)
    reference=$(awk '/"evidence_sources": \[/ {inside=1; next} inside && /"id":/ {line=$0; sub(/.*"id":[[:space:]]*"/, "", line); sub(/".*/, "", line); print line; exit}' "$prompt")
    result='{"version":1,"run_id":"'"$run_id"'","decisions":['
    first=yes
    roles=$(awk '/"candidates": \[/ {inside=1; next} inside && /\]/ {exit} inside && /"role":/ {line=$0; sub(/.*"role":[[:space:]]*"/, "", line); sub(/".*/, "", line); print line}' "$prompt")
    for role in $roles; do
      if [ "$first" = yes ]; then first=no; else result="$result,"; fi
      result="$result{\"role\":\"$role\",\"decision\":\"exclude\",\"evidence_refs\":[\"$reference\"],\"reason\":\"fixture\",\"confidence\":1}"
    done
    result="$result]}"
    ;;
  *candidate-findings*)
    run_id=$(run_id_from_block review-packet)
    role=$(awk -F': ' '/^Role: / {print $2; exit}' "$prompt")
    result='{"version":1,"run_id":"'"$run_id"'","role":"'"$role"'","findings":[]}'
    ;;
  *evidence-verdict*)
    run_id=$(run_id_from_block prechecked-candidates)
    {{EVIDENCE_FAILURE}}
    result='{"version":1,"run_id":"'"$run_id"'","verdicts":[]}'
    ;;
  *) result='{}' ;;
esac
printf '%s' "$result" > "$output"
escaped=$(printf '%s' "$result" | sed 's/\\/\\\\/g; s/"/\\"/g')
printf '{"type":"item.completed","item":{"type":"agent_message","text":"%s"}}\n{"type":"turn.completed"}\n' "$escaped"

`
	if evidenceFailure {
		script = strings.ReplaceAll(script, "{{EVIDENCE_FAILURE}}", "echo 'category=provider-unavailable' >&2; exit 1")
	} else {
		script = strings.ReplaceAll(script, "{{EVIDENCE_FAILURE}}", ":")
	}
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
}
