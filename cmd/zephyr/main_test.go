package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/signaturekey/zephyr/internal/schema"
)

func TestRecoverCodexOutputCmdWritesValidatedAgentMessage(t *testing.T) {
	directory := t.TempDir()
	eventsPath := filepath.Join(directory, "events.jsonl")
	outputPath := filepath.Join(directory, "last-message.json")
	message := `{"version":1,"run_id":"run-1","role":"architect-reviewer","findings":[]}`
	events := `{"type":"item.completed","item":{"type":"agent_message","text":"{\"version\":1,\"run_id\":\"run-1\",\"role\":\"architect-reviewer\",\"findings\":[]}"}}` + "\n" +
		`{"type":"turn.completed"}` + "\n"
	if err := os.WriteFile(eventsPath, []byte(events), 0o600); err != nil {
		t.Fatal(err)
	}

	command := RecoverCodexOutputCmd{Kind: "reviewer", Input: eventsPath, Output: outputPath}
	if err := command.Run(&runtime{ctx: context.Background()}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != message {
		t.Fatalf("output = %q, want %q", got, message)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("output mode = %o, want 600", info.Mode().Perm())
	}
}

func TestVersionCmdIncludesBuildAndProtocolMetadata(t *testing.T) {
	oldVersion, oldCommit, oldDirty := version, commit, dirty
	version, commit, dirty = "v1.2.3", "abc123", "false"
	t.Cleanup(func() {
		version, commit, dirty = oldVersion, oldCommit, oldDirty
	})

	var stdout bytes.Buffer
	if err := (&VersionCmd{}).Run(&runtime{stdout: &stdout}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var got struct {
		Version         string `json:"version"`
		Commit          string `json:"commit"`
		Dirty           string `json:"dirty"`
		ProtocolVersion int    `json:"protocol_version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode version output: %v", err)
	}
	if got.Version != "v1.2.3" || got.Commit != "abc123" || got.Dirty != "false" || got.ProtocolVersion != schema.ProtocolVersion {
		t.Fatalf("unexpected version output: %+v", got)
	}
}
