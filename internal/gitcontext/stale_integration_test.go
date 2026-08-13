package gitcontext

import (
	"context"
	"testing"

	"github.com/signaturekey/zephyr/internal/run"
	"github.com/stretchr/testify/require"
)

func TestCheckStaleDetectsWorkingTreeAndHEADChanges(t *testing.T) {
	repository := newTestRepository(t, "stale")
	repository.write(t, "file.txt", []byte("base\n"))
	repository.commitAll(t, "base")
	repository.append(t, "file.txt", "first change\n")
	collector := repository.collector(t)
	original, err := collector.Collect(context.Background(), Options{
		Repository: repository.path,
		Source:     run.SourceWorkingTree,
	})
	require.NoError(t, err)
	unchanged, err := collector.CheckStale(context.Background(), original)
	require.NoError(t, err)
	if unchanged.Stale {
		t.Fatalf("unchanged snapshot reported stale: %#v", unchanged)
	}

	repository.append(t, "file.txt", "second change\n")
	workingTreeChanged, err := collector.CheckStale(context.Background(), original)
	require.NoError(t, err)
	if !workingTreeChanged.Stale || !workingTreeChanged.WorkingTreeChanged || workingTreeChanged.HeadChanged {
		t.Fatalf("working-tree drift = %#v", workingTreeChanged)
	}

	repository.commitAll(t, "advance head")
	headChanged, err := collector.CheckStale(context.Background(), original)
	require.NoError(t, err)
	if !headChanged.Stale || !headChanged.HeadChanged || !headChanged.BaseChanged {
		t.Fatalf("HEAD drift = %#v", headChanged)
	}
}

func TestCheckStaleForStagedIgnoresUnstagedChangesButDetectsIndexChanges(t *testing.T) {
	repository := newTestRepository(t, "staged-stale")
	repository.write(t, "staged.txt", []byte("base\n"))
	repository.write(t, "other.txt", []byte("base\n"))
	repository.commitAll(t, "base")
	repository.append(t, "staged.txt", "staged change\n")
	repository.git(t, "add", "--", "staged.txt")
	collector := repository.collector(t)
	original, err := collector.Collect(context.Background(), Options{
		Repository: repository.path,
		Source:     run.SourceStaged,
	})
	require.NoError(t, err)
	if original.WorkingTreeFingerprint != "" {
		t.Fatalf("staged snapshot captured a working-tree fingerprint: %q", original.WorkingTreeFingerprint)
	}

	repository.append(t, "other.txt", "unstaged change\n")
	repository.write(t, "untracked.txt", []byte("untracked change\n"))
	unstagedOnly, err := collector.CheckStale(context.Background(), original)
	require.NoError(t, err)
	if unstagedOnly.Stale || unstagedOnly.WorkingTreeChanged || unstagedOnly.SourceChanged {
		t.Fatalf("staged snapshot reacted to out-of-scope changes: %#v", unstagedOnly)
	}

	repository.git(t, "add", "--", "other.txt")
	indexChanged, err := collector.CheckStale(context.Background(), original)
	require.NoError(t, err)
	if !indexChanged.Stale || !indexChanged.SourceChanged || indexChanged.WorkingTreeChanged || indexChanged.HeadChanged {
		t.Fatalf("staged index drift = %#v", indexChanged)
	}
}

func TestCheckStaleForCommitRangeIgnoresWorkingTreeChanges(t *testing.T) {
	repository := newTestRepository(t, "commit-range-stale")
	repository.write(t, "base.txt", []byte("base\n"))
	from := repository.commitAll(t, "base")
	repository.write(t, "range.txt", []byte("range change\n"))
	to := repository.commitAll(t, "range")
	collector := repository.collector(t)
	original, err := collector.Collect(context.Background(), Options{
		Repository:  repository.path,
		Source:      run.SourceCommitRange,
		CommitRange: from + ".." + to,
	})
	require.NoError(t, err)
	if original.WorkingTreeFingerprint != "" {
		t.Fatalf("commit-range snapshot captured a working-tree fingerprint: %q", original.WorkingTreeFingerprint)
	}

	repository.append(t, "base.txt", "dirty change\n")
	repository.write(t, "untracked.txt", []byte("untracked change\n"))
	current, err := collector.CheckStale(context.Background(), original)
	require.NoError(t, err)
	if current.Stale || current.WorkingTreeChanged || current.SourceChanged {
		t.Fatalf("commit-range snapshot reacted to out-of-scope changes: %#v", current)
	}
}
