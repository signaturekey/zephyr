package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		Version         string `json:"version"`
		Commit          string `json:"commit"`
		Dirty           string `json:"dirty"`
		ProtocolVersion int    `json:"protocol_version"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.Equal(t, "v1.2.3", got.Version)
	assert.Equal(t, "abc123", got.Commit)
	assert.Equal(t, "false", got.Dirty)
	assert.Equal(t, schema.ProtocolVersion, got.ProtocolVersion)
}
