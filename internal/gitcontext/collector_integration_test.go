package gitcontext

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/signaturekey/zephyr/internal/run"
	"github.com/stretchr/testify/require"
)

func TestCollectWorkingTreeCombinesChangesWithoutDuplication(t *testing.T) {
	repository := newTestRepository(t, "repo with spaces")
	repository.write(t, "regular.txt", []byte("base\n"))
	repository.write(t, "delete.txt", []byte("delete me\n"))
	repository.write(t, "rename.txt", []byte("rename me\n"))
	repository.write(t, "binary.bin", []byte{0, 1, 2})
	repository.write(t, "generated/value.pb.go", []byte("package generated\n"))
	repository.write(t, ".env.local", []byte("OLD_SECRET\n"))
	repository.commitAll(t, "initial")

	repository.write(t, "regular.txt", []byte("base\nstaged\n"))
	repository.git(t, "add", "--", "regular.txt")
	repository.append(t, "regular.txt", "unstaged\n")
	repository.git(t, "mv", "--", "rename.txt", "renamed file.txt")
	require.NoError(t, os.Remove(filepath.Join(repository.path, "delete.txt")))
	repository.write(t, "binary.bin", []byte{0, 8, 9, 10})
	repository.git(t, "add", "--", "binary.bin")
	repository.write(t, "generated/value.pb.go", []byte("package generated\n// changed\n"))
	repository.write(t, ".env.local", []byte("NEW_SECRET_MUST_NOT_LEAK\n"))
	untrackedPath := "odd name\nfile.go"
	repository.write(t, untrackedPath, []byte("UNTRACKED_SENTINEL\n"))

	statusBefore := repository.git(t, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	snapshot, err := repository.collector(t).Collect(context.Background(), Options{
		Repository: repository.path,
		Source:     run.SourceWorkingTree,
	})
	require.NoError(t, err)
	statusAfter := repository.git(t, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if statusBefore != statusAfter {
		t.Fatal("collector changed the Git working tree or index")
	}
	if snapshot.Repository.Branch != "main" || snapshot.Repository.HeadSHA == "" {
		t.Fatalf("unexpected repository metadata: %#v", snapshot.Repository)
	}
	if strings.Count(snapshot.Patches.Full, "+staged") != 1 {
		t.Fatalf("combined patch duplicates or misses staged content:\n%s", snapshot.Patches.Full)
	}
	if strings.Count(snapshot.Patches.Full, "+unstaged") != 1 {
		t.Fatalf("combined patch duplicates or misses unstaged content:\n%s", snapshot.Patches.Full)
	}
	if !strings.Contains(snapshot.Patches.Staged, "+staged") || strings.Contains(snapshot.Patches.Staged, "+unstaged") {
		t.Fatalf("unexpected staged patch:\n%s", snapshot.Patches.Staged)
	}
	if !strings.Contains(snapshot.Patches.Unstaged, "+unstaged") {
		t.Fatalf("unexpected unstaged patch:\n%s", snapshot.Patches.Unstaged)
	}
	if strings.Contains(snapshot.Patches.Full, "UNTRACKED_SENTINEL") {
		t.Fatal("default collection included untracked content")
	}
	if findUntracked(t, snapshot, untrackedPath).ContentIncluded {
		t.Fatal("default collection marked untracked content as included")
	}

	rename := findChange(t, snapshot, "renamed file.txt")
	if rename.Status != ChangeRenamed || rename.PreviousPath != "rename.txt" {
		t.Fatalf("rename metadata = %#v", rename)
	}
	deleted := findChange(t, snapshot, "delete.txt")
	if deleted.Status != ChangeDeleted || !strings.Contains(snapshot.Patches.Full, "delete me") {
		t.Fatalf("delete metadata/patch is incomplete: %#v", deleted)
	}
	binary := findChange(t, snapshot, "binary.bin")
	if !binary.Binary || binary.ContentIncluded || binary.ExclusionReason != "binary" {
		t.Fatalf("binary metadata = %#v", binary)
	}
	if strings.Contains(snapshot.Patches.Full, "binary.bin") {
		t.Fatal("binary patch body was not excluded")
	}
	generated := findChange(t, snapshot, "generated/value.pb.go")
	if !generated.Generated || generated.ContentIncluded || generated.ExclusionReason != "generated" {
		t.Fatalf("generated metadata = %#v", generated)
	}
	if strings.Contains(snapshot.Patches.Full, "generated/value.pb.go") {
		t.Fatal("generated body was not excluded by default")
	}
	restricted := findChange(t, snapshot, ".env.local")
	if !restricted.Restricted || restricted.ContentIncluded || restricted.ExclusionReason != "restricted" {
		t.Fatalf("restricted metadata = %#v", restricted)
	}
	if strings.Contains(snapshot.Patches.Full, "NEW_SECRET_MUST_NOT_LEAK") || strings.Contains(snapshot.Patches.Full, ".env.local") {
		t.Fatal("restricted path or content leaked into the patch")
	}
	assertSortedChanges(t, snapshot.Changes)
	if snapshot.Stats.Files != len(snapshot.Changes) || snapshot.Stats.Untracked != 1 {
		t.Fatalf("unexpected stats: %#v", snapshot.Stats)
	}
}

func TestCollectorDisablesRepositoryFSMonitorHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fsmonitor fixture is Unix-specific")
	}
	repository := newTestRepository(t, "hostile-fsmonitor")
	repository.write(t, "base.txt", []byte("base\n"))
	repository.commitAll(t, "base")
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	hook := filepath.Join(t.TempDir(), "fsmonitor")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\ntouch \""+marker+"\"\nprintf '0\\n'\n"), 0o700))
	repository.git(t, "config", "core.fsmonitor", hook)
	repository.write(t, "base.txt", []byte("changed\n"))

	_, err := repository.collector(t).Collect(context.Background(), Options{
		Repository: repository.path,
		Source:     run.SourceWorkingTree,
	})
	require.NoError(t, err)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("repository fsmonitor hook executed; stat error = %v", err)
	}
}

func TestCollectorRejectsExternalFiltersBeforeWorkingTreeCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable filter fixture is Unix-specific")
	}
	for _, field := range []string{"clean", "process"} {
		for _, includedConfig := range []bool{false, true} {
			name := field + "-local"
			if includedConfig {
				name = field + "-included"
			}
			t.Run(name, func(t *testing.T) {
				repository := newTestRepository(t, "hostile-filter-"+name)
				repository.write(t, ".gitattributes", []byte("*.txt filter=zephyraudit\n"))
				repository.write(t, "base.txt", []byte("base\n"))
				repository.commitAll(t, "base with inert filter attribute")

				marker := filepath.Join(t.TempDir(), "filter-ran")
				filter := filepath.Join(t.TempDir(), "content-filter")
				require.NoError(t, os.WriteFile(filter, []byte("#!/bin/sh\ntouch \""+marker+"\"\ncat\n"), 0o700))
				if includedConfig {
					include := filepath.Join(t.TempDir(), "filters.config")
					require.NoError(t, os.WriteFile(include, []byte("[filter \"zephyraudit\"]\n\t"+field+" = "+filter+"\n"), 0o600))
					repository.git(t, "config", "include.path", include)
				} else {
					repository.git(t, "config", "filter.zephyraudit."+field, filter)
				}
				repository.write(t, "base.txt", []byte("changed\n"))

				_, err := repository.collector(t).Collect(context.Background(), Options{
					Repository: repository.path,
					Source:     run.SourceWorkingTree,
				})
				require.ErrorIs(t, err, ErrUnsafeGitConfig)
				if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("external %s filter executed; marker stat error = %v", field, statErr)
				}

				planOnly, planErr := repository.collector(t).Collect(context.Background(), Options{
					Repository: repository.path,
					Source:     run.SourcePlanOnly,
				})
				require.NoError(t, planErr, "plan-only collection must skip working-tree filters")
				if len(planOnly.Status.Entries) != 0 || len(planOnly.Status.Untracked) != 0 {
					t.Fatalf("plan-only collection inspected worktree status: %#v", planOnly.Status)
				}
				if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("plan-only collection executed %s filter; marker stat error = %v", field, statErr)
				}
			})
		}
	}
}

func TestCollectorAllowsIndexAndObjectDiffsWithConfiguredFilters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable filter fixture is Unix-specific")
	}
	for _, field := range []string{"clean", "process"} {
		for _, source := range []run.Source{run.SourceStaged, run.SourceCommitRange} {
			t.Run(field+"-"+string(source), func(t *testing.T) {
				repository := newTestRepository(t, "safe-filter-scope-"+field+"-"+string(source))
				repository.write(t, ".gitattributes", []byte("*.txt filter=zephyraudit\n"))
				repository.write(t, "reviewed.txt", []byte("base\n"))
				repository.write(t, "ignored.txt", []byte("base\n"))
				from := repository.commitAll(t, "base with inert filter attribute")

				repository.write(t, "reviewed.txt", []byte("reviewed change\n"))
				options := Options{Repository: repository.path, Source: source}
				if source == run.SourceStaged {
					repository.git(t, "add", "--", "reviewed.txt")
				} else {
					to := repository.commitAll(t, "range change")
					options.CommitRange = from + ".." + to
				}

				marker := filepath.Join(t.TempDir(), "filter-ran")
				filter := filepath.Join(t.TempDir(), "content-filter")
				require.NoError(t, os.WriteFile(filter, []byte("#!/bin/sh\ntouch \""+marker+"\"\ncat\n"), 0o700))
				repository.git(t, "config", "filter.zephyraudit."+field, filter)
				repository.write(t, "ignored.txt", []byte("unrelated dirty change\n"))

				snapshot, err := repository.collector(t).Collect(context.Background(), options)
				require.NoError(t, err)
				if !strings.Contains(snapshot.Patches.Full, "reviewed change") {
					t.Fatalf("%s patch misses reviewed content:\n%s", source, snapshot.Patches.Full)
				}
				if snapshot.WorkingTreeFingerprint != "" || len(snapshot.Status.Entries) != 0 || len(snapshot.Status.Untracked) != 0 {
					t.Fatalf("%s unexpectedly captured the working tree: %#v", source, snapshot)
				}
				if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("index/object-only %s collection executed %s filter; marker stat error = %v", source, field, statErr)
				}
			})
		}
	}
}

func TestCollectStagedExcludesUnstagedDiff(t *testing.T) {
	repository := newTestRepository(t, "staged")
	repository.write(t, "staged.txt", []byte("old\n"))
	repository.write(t, "unstaged.txt", []byte("old\n"))
	repository.commitAll(t, "initial")
	repository.write(t, "staged.txt", []byte("old\nstaged-only\n"))
	repository.git(t, "add", "--", "staged.txt")
	repository.write(t, "unstaged.txt", []byte("old\nunstaged-only\n"))

	snapshot, err := repository.collector(t).Collect(context.Background(), Options{
		Repository: repository.path,
		Source:     run.SourceStaged,
	})
	require.NoError(t, err)
	if !strings.Contains(snapshot.Patches.Full, "staged-only") || strings.Contains(snapshot.Patches.Full, "unstaged-only") {
		t.Fatalf("staged scope patch is wrong:\n%s", snapshot.Patches.Full)
	}
	if snapshot.Patches.Staged != snapshot.Patches.Full || snapshot.Patches.Unstaged != "" {
		t.Fatalf("staged/unstaged artifacts are wrong: %#v", snapshot.Patches)
	}
	if len(snapshot.Changes) != 1 || snapshot.Changes[0].Path != "staged.txt" {
		t.Fatalf("staged changes = %#v", snapshot.Changes)
	}
}

func TestCollectBranchIncludesCommittedAndWorkingTreeChanges(t *testing.T) {
	repository := newTestRepository(t, "branch")
	repository.write(t, "base.txt", []byte("base\n"))
	baseSHA := repository.commitAll(t, "base")
	repository.git(t, "checkout", "-q", "-b", "feature")
	repository.write(t, "committed.txt", []byte("from commit\n"))
	repository.commitAll(t, "feature commit")
	repository.append(t, "base.txt", "from working tree\n")

	snapshot, err := repository.collector(t).Collect(context.Background(), Options{
		Repository: repository.path,
		Source:     run.SourceBranch,
		BaseRef:    "main",
	})
	require.NoError(t, err)
	if snapshot.Repository.MergeBaseSHA != baseSHA || snapshot.Repository.BaseSHA != baseSHA {
		t.Fatalf("branch base metadata = %#v, want %s", snapshot.Repository, baseSHA)
	}
	if !strings.Contains(snapshot.Patches.Full, "from commit") || !strings.Contains(snapshot.Patches.Full, "from working tree") {
		t.Fatalf("branch patch misses committed or local changes:\n%s", snapshot.Patches.Full)
	}
}

func TestCollectCommitRangeIgnoresWorkingTree(t *testing.T) {
	repository := newTestRepository(t, "range")
	repository.write(t, "base.txt", []byte("base\n"))
	from := repository.commitAll(t, "base")
	repository.write(t, "range.txt", []byte("range content\n"))
	to := repository.commitAll(t, "range")
	repository.append(t, "base.txt", "dirty content\n")

	snapshot, err := repository.collector(t).Collect(context.Background(), Options{
		Repository:  repository.path,
		Source:      run.SourceCommitRange,
		CommitRange: from + ".." + to,
	})
	require.NoError(t, err)
	if snapshot.Repository.BaseSHA != from || snapshot.Repository.TargetSHA != to {
		t.Fatalf("range metadata = %#v", snapshot.Repository)
	}
	if !strings.Contains(snapshot.Patches.Full, "range content") || strings.Contains(snapshot.Patches.Full, "dirty content") {
		t.Fatalf("commit-range patch is wrong:\n%s", snapshot.Patches.Full)
	}
	if snapshot.Patches.Staged != "" || snapshot.Patches.Unstaged != "" {
		t.Fatalf("commit range has local patch artifacts: %#v", snapshot.Patches)
	}
}

func TestCollectPlanOnlyHasMetadataWithoutDiff(t *testing.T) {
	repository := newTestRepository(t, "plan")
	repository.write(t, "base.txt", []byte("base\n"))
	repository.commitAll(t, "base")
	repository.write(t, "untracked.txt", []byte("not reviewed\n"))

	snapshot, err := repository.collector(t).Collect(context.Background(), Options{
		Repository: repository.path,
		Source:     run.SourcePlanOnly,
	})
	require.NoError(t, err)
	if snapshot.Patches != (Patches{}) || len(snapshot.Changes) != 0 {
		t.Fatalf("plan-only snapshot contains a diff: %#v", snapshot)
	}
	if len(snapshot.Status.Entries) != 0 || len(snapshot.Status.Untracked) != 0 {
		t.Fatalf("plan-only must not inspect working-tree status: %#v", snapshot.Status)
	}
}

func TestCollectPlanOnlySupportsUnbornBranch(t *testing.T) {
	repository := newTestRepository(t, "unborn")
	repository.write(t, "REVIEW_SPEC.md", []byte("# Plan\n"))
	snapshot, err := repository.collector(t).Collect(context.Background(), Options{
		Repository: repository.path,
		Source:     run.SourcePlanOnly,
	})
	require.NoError(t, err)
	if snapshot.Repository.HeadSHA != "" || snapshot.Repository.Detached || snapshot.Repository.Branch != "main" {
		t.Fatalf("unborn branch metadata = %#v", snapshot.Repository)
	}
	if len(snapshot.Limitations) != 1 || snapshot.Limitations[0] != "repository has no HEAD commit" {
		t.Fatalf("unborn limitations = %#v", snapshot.Limitations)
	}
}
