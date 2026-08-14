package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/signaturekey/zephyr/internal/protocol"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/signaturekey/zephyr/internal/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteCommandErrorJSONIsSafe(t *testing.T) {
	var output bytes.Buffer
	injected := "/private/repository and {invalid-json}"
	writeCommandError(&output, "json", "validate-candidates", fmt.Errorf("wrapped: %w: %s", schema.ErrInvalidDocument, injected))
	var envelope ErrorEnvelope
	require.NoError(t, json.Unmarshal(output.Bytes(), &envelope))
	assert.Equal(t, ErrorEnvelope{Version: 1, Operation: "validate-candidates", Kind: "validation", ReasonCode: "invalid-agent-output"}, envelope)
	assert.NotContains(t, output.String(), injected)

	output.Reset()
	writeCommandError(&output, "json", "collect", errors.New(injected))
	require.NoError(t, json.Unmarshal(output.Bytes(), &envelope))
	assert.Equal(t, "operation", envelope.Kind)
	assert.NotContains(t, output.String(), injected)
}

func TestRecoverCodexOutputCmdWritesValidatedAgentMessage(t *testing.T) {
	directory := t.TempDir()
	eventsPath := filepath.Join(directory, "events.jsonl")
	outputPath := filepath.Join(directory, "last-message.json")
	message := `{"version":1,"run_id":"run-1","role":"architect-reviewer","findings":[]}`
	events := `{"type":"item.completed","item":{"type":"agent_message","text":"{\"version\":1,\"run_id\":\"run-1\",\"role\":\"architect-reviewer\",\"findings\":[]}"}}` + "\n" +
		`{"type":"turn.completed"}` + "\n"
	require.NoError(t, os.WriteFile(eventsPath, []byte(events), 0o600))

	command := RecoverCodexOutputCmd{Kind: "reviewer", Input: eventsPath, Output: outputPath}
	require.NoError(t, command.Run(&runtime{ctx: context.Background()}))
	got, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, message, string(got))
	info, err := os.Stat(outputPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestVersionCmdIncludesBuildAndProtocolMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDirty := version, commit, dirty
	version, commit, dirty = "v1.2.3", "abc123", "false"
	t.Cleanup(func() {
		version, commit, dirty = oldVersion, oldCommit, oldDirty
	})

	var stdout bytes.Buffer
	require.NoError(t, (&VersionCmd{}).Run(&runtime{stdout: &stdout}))

	var got struct {
		Version                string `json:"version"`
		Commit                 string `json:"commit"`
		Dirty                  string `json:"dirty"`
		ProtocolVersion        int    `json:"protocol_version"`
		CodexHarnessAPIVersion int    `json:"codex_harness_api_version"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.Equal(t, "v1.2.3", got.Version)
	assert.Equal(t, "abc123", got.Commit)
	assert.Equal(t, "false", got.Dirty)
	assert.Equal(t, schema.ProtocolVersion, got.ProtocolVersion)
	assert.Equal(t, protocol.CodexHarnessAPIVersion, got.CodexHarnessAPIVersion)

	var missing struct {
		ProtocolVersion        int `json:"protocol_version"`
		CodexHarnessAPIVersion int `json:"codex_harness_api_version"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"protocol_version":1}`), &missing))
	assert.Equal(t, schema.ProtocolVersion, missing.ProtocolVersion)
	assert.NotEqual(t, protocol.CodexHarnessAPIVersion, missing.CodexHarnessAPIVersion)

	var changed struct {
		ProtocolVersion        int `json:"protocol_version"`
		CodexHarnessAPIVersion int `json:"codex_harness_api_version"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"protocol_version":1,"codex_harness_api_version":99}`), &changed))
	assert.Equal(t, schema.ProtocolVersion, changed.ProtocolVersion)
	assert.NotEqual(t, protocol.CodexHarnessAPIVersion, changed.CodexHarnessAPIVersion)
}

func TestPrepareEvidenceCommandIsRecognized(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runMain([]string{"--run-root", t.TempDir(), "prepare-evidence", "--run", "run-1"}, bytes.NewReader(nil), &stdout, &stderr)
	assert.Equal(t, 1, exitCode)
	assert.NotContains(t, stderr.String(), "unknown command")
	assert.Contains(t, stderr.String(), "run-1")
}

func TestRunPrepareEvidenceEmitsSuccessfulArtifactResult(t *testing.T) {
	var stdout bytes.Buffer
	want := workflow.PrepareEvidenceResult{
		RunID:        "run-1",
		CandidateSet: "/private/run/evidence/prechecked.json",
		Evidence:     "/private/run/evidence/minimal.json",
		Items:        1,
	}

	err := runPrepareEvidence(context.Background(), &stdout, "run-1", func(_ context.Context, options workflow.PrepareEvidenceOptions) (workflow.PrepareEvidenceResult, error) {
		assert.Equal(t, workflow.PrepareEvidenceOptions{RunID: "run-1"}, options)
		return want, nil
	})

	require.NoError(t, err)
	var got workflow.PrepareEvidenceResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.Equal(t, want, got)
}
