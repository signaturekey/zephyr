package gitcontext

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/signaturekey/zephyr/internal/run"
	"github.com/stretchr/testify/require"
)

func TestCollectMarksSubmoduleChanges(t *testing.T) {
	source := newTestRepository(t, "submodule-source")
	source.write(t, "value.txt", []byte("v1\n"))
	source.commitAll(t, "v1")

	parent := newTestRepository(t, "parent")
	parent.write(t, "base.txt", []byte("base\n"))
	parent.commitAll(t, "base")
	parent.git(t, "-c", "protocol.file.allow=always", "submodule", "add", "-q", "--", source.path, "modules/sample")
	parent.commitAll(t, "add submodule")

	source.write(t, "value.txt", []byte("v2\n"))
	newSHA := source.commitAll(t, "v2")
	checkedOutSubmodule := testRepository{path: parent.path + "/modules/sample", env: parent.env}
	checkedOutSubmodule.git(t, "-c", "protocol.file.allow=always", "fetch", "-q", "origin")
	checkedOutSubmodule.git(t, "checkout", "-q", newSHA)
	marker := ""
	if runtime.GOOS != "windows" {
		marker = filepath.Join(t.TempDir(), "submodule-filter-ran")
		filter := filepath.Join(t.TempDir(), "submodule-clean-filter")
		require.NoError(t, os.WriteFile(filter, []byte("#!/bin/sh\ntouch \""+marker+"\"\ncat\n"), 0o700))
		checkedOutSubmodule.git(t, "config", "filter.zephyraudit.clean", filter)
		checkedOutSubmodule.write(t, ".gitattributes", []byte("*.txt filter=zephyraudit\n"))
		checkedOutSubmodule.write(t, "value.txt", []byte("locally dirty\n"))
	}

	snapshot, err := parent.collector(t).Collect(context.Background(), Options{
		Repository: parent.path,
		Source:     run.SourceWorkingTree,
	})
	require.NoError(t, err)
	change := findChange(t, snapshot, "modules/sample")
	if !change.Submodule || change.OldMode != "160000" || change.NewMode != "160000" {
		t.Fatalf("submodule metadata = %#v", change)
	}
	if snapshot.Stats.Submodules != 1 {
		t.Fatalf("submodule stats = %#v", snapshot.Stats)
	}
	if marker != "" {
		if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("submodule clean filter executed; marker stat error = %v", statErr)
		}
	}
}
