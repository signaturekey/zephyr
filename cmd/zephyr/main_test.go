package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/signaturekey/zephyr/internal/schema"
)

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
