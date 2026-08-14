package review

import (
	"context"
	"testing"
	"time"

	"github.com/signaturekey/zephyr/internal/codexharness/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type coreRunner struct {
	request process.Request
	result  process.Result
	err     error
}

func (runner *coreRunner) Run(_ context.Context, request process.Request) (process.Result, error) {
	runner.request = request
	return runner.result, runner.err
}

func TestCoreClientInitUsesExactSafeArgv(t *testing.T) {
	runner := &coreRunner{result: process.Result{Stdout: []byte(`{"run_id":"run-1","run_dir":"/tmp/run-1","manifest":"/tmp/run-1/manifest.json"}`)}}
	client := NewCoreClient("/usr/local/bin/zephyr", "", runner)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := client.Init(ctx, "/work/repository")
	require.NoError(t, err)
	assert.Equal(t, "run-1", result.RunID)
	assert.Equal(t, []string{"--error-format", "json", "init", "--repo", "/work/repository", "--mode", "implementation", "--source", "working-tree"}, runner.request.Args)
	assert.Positive(t, runner.request.Timeout)
}

func TestCoreClientRejectsUnknownJSONFields(t *testing.T) {
	runner := &coreRunner{result: process.Result{Stdout: []byte(`{"run_id":"run-1","unexpected":true}`)}}
	client := NewCoreClient("/usr/local/bin/zephyr", "", runner)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := client.Inspect(ctx, "run-1")
	var coreErr *CoreError
	require.ErrorAs(t, err, &coreErr)
	assert.Equal(t, CoreErrorProtocol, coreErr.Kind)
}

func TestCoreClientAggregateAcceptsRejectedPath(t *testing.T) {
	runner := &coreRunner{result: process.Result{Stdout: []byte(`{"run_id":"run-1","status":"complete","findings":0,"needs_human":0,"stale":false,"review_path":"/tmp/review.json","rejected_path":"/tmp/rejected.json"}`)}}
	client := NewCoreClient("/usr/local/bin/zephyr", "", runner)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	result, err := client.Aggregate(ctx, "run-1")

	require.NoError(t, err)
	assert.Equal(t, "/tmp/rejected.json", result.RejectedPath)
}

func TestCoreClientInspectAcceptsCoreContract(t *testing.T) {
	runner := &coreRunner{result: process.Result{Stdout: []byte(`{
"run_id":"run-1","run_dir":"/tmp/run-1","state":"complete","mode":"implementation","source":"working-tree",
"stages":[{"name":"init","state":"complete"}],"capabilities":[{"source":"jira","status":"not-required"}],"coverage_limits":["none"],"staleness":{"stale":false},
"counts":{"selected_roles":1,"validated_roles":1,"failed_roles":0,"confirmed_findings":0,"needs_human":0,"by_severity":{"P1":0}},
"artifacts":{"manifest":"/tmp/manifest.json","snapshot":"/tmp/snapshot.json","capabilities":"/tmp/capabilities.json","model_policy":"/tmp/model-policy.json","packet":"/tmp/packet.json","routing":"/tmp/routing.json","routing_request":"/tmp/routing-request.json","candidates":"/tmp/candidates.json","minimal_evidence":"/tmp/minimal.json","verdicts":"/tmp/verdicts.json","review_json":"/tmp/review.json","review_markdown":"/tmp/review.md","rejected":"/tmp/rejected.json","trace":"/tmp/trace.json"},
"routing":{"selected":[]},"review":{"version":1}
}`)}}
	client := NewCoreClient("/usr/local/bin/zephyr", "", runner)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	result, err := client.Inspect(ctx, "run-1")

	require.NoError(t, err)
	assert.Equal(t, "/tmp/run-1", result.RunDir)
	assert.Equal(t, "/tmp/rejected.json", result.Artifacts.Rejected)
	assert.Equal(t, 1, result.Counts.SelectedRoles)
}
