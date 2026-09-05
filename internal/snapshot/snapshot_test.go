package snapshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireWorktreeCombinesTrackedAndUntrackedChanges(t *testing.T) {
	repo := newRepository(t)
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n\nconst value = 1\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "initial")

	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n\nconst value = 2\n")
	gitCommand(t, repo, "add", "main.go")
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n\nconst value = 3\n")
	writeFile(t, filepath.Join(repo, "new.go"), "package demo\n")
	before := gitCommand(t, repo, "status", "--porcelain")

	snapshot, err := Acquire(context.Background(), Request{Repository: repo, Source: SourceWorktree})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshot.Cleanup()) })

	content, err := os.ReadFile(filepath.Join(snapshot.Root, "main.go"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "value = 3")
	_, err = os.Stat(filepath.Join(snapshot.Root, "new.go"))
	require.NoError(t, err)
	assert.Equal(t, []string{"main.go", "new.go"}, snapshot.ChangedPaths)
	assert.Equal(t, []string{"new.go"}, snapshot.Untracked)
	assert.Contains(t, snapshot.Diff, "+const value = 3")
	assert.Contains(t, snapshot.Diff, "b/new.go")
	assert.Equal(t, before, gitCommand(t, repo, "status", "--porcelain"))
}

func TestAcquireWorktreeUsesFrozenSnapshotForChangedPaths(t *testing.T) {
	repo := newRepository(t)
	writeFile(t, filepath.Join(repo, "f.go"), "package demo\n\nconst f = 1\n")
	writeFile(t, filepath.Join(repo, "g.go"), "package demo\n\nconst g = 1\n")
	gitCommand(t, repo, "add", "f.go", "g.go")
	gitCommand(t, repo, "commit", "-m", "initial")
	writeFile(t, filepath.Join(repo, "f.go"), "package demo\n\nconst f = 2\n")

	realGit, err := exec.LookPath("git")
	require.NoError(t, err)
	wrapperDir := t.TempDir()
	wrapper := filepath.Join(wrapperDir, "git")
	script := "#!/bin/sh\n" +
		"\"$ZEPHYR_REAL_GIT\" \"$@\"\n" +
		"status=$?\n" +
		"if [ \"$status\" -eq 0 ] && [ \"$1\" = \"-C\" ] && [ \"$2\" = \"$ZEPHYR_TEST_REPOSITORY\" ] && [ \"$3\" = \"diff\" ] && [ \"$4\" = \"--binary\" ]; then\n" +
		"  \"$ZEPHYR_REAL_GIT\" -C \"$ZEPHYR_TEST_REPOSITORY\" checkout -- f.go\n" +
		"  printf 'package demo\\n\\nconst g = 2\\n' > \"$ZEPHYR_TEST_REPOSITORY/g.go\"\n" +
		"fi\n" +
		"exit \"$status\"\n"
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0o700))
	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ZEPHYR_REAL_GIT", realGit)
	t.Setenv("ZEPHYR_TEST_REPOSITORY", repo)

	snapshot, err := Acquire(context.Background(), Request{Repository: repo, Source: SourceWorktree})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshot.Cleanup()) })

	assert.Equal(t, []string{"f.go"}, snapshot.ChangedPaths)
	assert.Contains(t, snapshot.Diff, "+const f = 2")
}

func TestAcquireCommitAndBranch(t *testing.T) {
	repo := newRepository(t)
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(gitCommand(t, repo, "rev-parse", "HEAD"))
	gitCommand(t, repo, "switch", "-c", "feature")
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n\nconst feature = true\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "feature")
	head := strings.TrimSpace(gitCommand(t, repo, "rev-parse", "HEAD"))

	commitSnapshot, err := Acquire(context.Background(), Request{Repository: repo, Source: SourceCommit, Commit: head})
	require.NoError(t, err)
	assert.Equal(t, head, commitSnapshot.HeadSHA)
	assert.Equal(t, base, commitSnapshot.BaseSHA)
	assert.Equal(t, []string{"main.go"}, commitSnapshot.ChangedPaths)
	require.NoError(t, commitSnapshot.Cleanup())

	branchSnapshot, err := Acquire(context.Background(), Request{Repository: repo, Source: SourceBranch, Branch: "feature", Base: base})
	require.NoError(t, err)
	assert.Equal(t, head, branchSnapshot.HeadSHA)
	assert.Equal(t, base, branchSnapshot.MergeBase)
	assert.Contains(t, branchSnapshot.Diff, "+const feature = true")
	require.NoError(t, branchSnapshot.Cleanup())
}

func TestAcquireBranchResolvesRefsInSourceRepository(t *testing.T) {
	repo := newRepository(t)
	gitCommand(t, repo, "branch", "-M", "main")
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n\nconst value = 1\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(gitCommand(t, repo, "rev-parse", "HEAD"))

	gitCommand(t, repo, "switch", "-c", "upstream")
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n\nconst value = 2\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "upstream")
	upstream := strings.TrimSpace(gitCommand(t, repo, "rev-parse", "HEAD"))
	gitCommand(t, repo, "switch", "main")
	gitCommand(t, repo, "update-ref", "refs/remotes/origin/main", upstream)

	snapshot, err := Acquire(context.Background(), Request{Repository: repo, Source: SourceBranch, Branch: "main", Base: "origin/main"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshot.Cleanup()) })

	assert.Equal(t, base, snapshot.HeadSHA)
	assert.Equal(t, upstream, snapshot.BaseSHA)
	assert.Equal(t, base, snapshot.MergeBase)
}

func TestAcquireBranchSupportsFullHeadRef(t *testing.T) {
	repo := newRepository(t)
	baseBranch := strings.TrimSpace(gitCommand(t, repo, "branch", "--show-current"))
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "base")
	base := strings.TrimSpace(gitCommand(t, repo, "rev-parse", "HEAD"))
	gitCommand(t, repo, "switch", "-c", "feature")
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n\nconst feature = true\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "feature")
	head := strings.TrimSpace(gitCommand(t, repo, "rev-parse", "HEAD"))
	gitCommand(t, repo, "switch", baseBranch)

	snapshot, err := Acquire(context.Background(), Request{Repository: repo, Source: SourceBranch, Branch: "refs/heads/feature", Base: base})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshot.Cleanup()) })

	assert.Equal(t, head, snapshot.HeadSHA)
	assert.Equal(t, base, snapshot.MergeBase)
	assert.Contains(t, snapshot.Diff, "+const feature = true")
}

func TestAcquireCommitIgnoresInheritedGitDirectory(t *testing.T) {
	repo := newRepository(t)
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n\nconst value = 1\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "first")
	first := strings.TrimSpace(gitCommand(t, repo, "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n\nconst value = 2\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "second")

	gitDir := filepath.Join(repo, ".git")
	headBefore, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	require.NoError(t, err)
	indexBefore, err := os.ReadFile(filepath.Join(gitDir, "index"))
	require.NoError(t, err)
	t.Setenv("GIT_DIR", gitDir)

	snapshot, err := Acquire(context.Background(), Request{Repository: repo, Source: SourceCommit, Commit: first})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, snapshot.Cleanup()) })

	headAfter, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	require.NoError(t, err)
	indexAfter, err := os.ReadFile(filepath.Join(gitDir, "index"))
	require.NoError(t, err)
	assert.Equal(t, headBefore, headAfter)
	assert.Equal(t, indexBefore, indexAfter)
	assert.Equal(t, first, snapshot.HeadSHA)
}

func TestAcquireWorktreeRejectsEscapingUntrackedSymlink(t *testing.T) {
	repo := newRepository(t)
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "initial")
	require.NoError(t, os.Symlink("../outside", filepath.Join(repo, "escape")))

	_, err := Acquire(context.Background(), Request{Repository: repo, Source: SourceWorktree})
	require.ErrorContains(t, err, "escapes snapshot root")
}

func TestCloneUsesExactRepositoryWithoutTransportFallback(t *testing.T) {
	fakeGitDir := t.TempDir()
	fakeGit := filepath.Join(fakeGitDir, "git")
	callLog := filepath.Join(fakeGitDir, "calls")
	repository := "https://example.test/org/repository.git"
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$6\" >> \"" + callLog + "\"\n" +
		"exit 1\n"
	require.NoError(t, os.WriteFile(fakeGit, []byte(script), 0o700))
	t.Setenv("PATH", fakeGitDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := clone(context.Background(), repository, t.TempDir())
	require.ErrorContains(t, err, `clone "`+repository+`"`)
	calls, readErr := os.ReadFile(callLog)
	require.NoError(t, readErr)
	assert.Equal(t, repository+"\n", string(calls))
}

func newRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitCommand(t, repo, "init", "-q")
	gitCommand(t, repo, "config", "user.email", "test@example.com")
	gitCommand(t, repo, "config", "user.name", "Test")
	return repo
}

func gitCommand(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	return string(output)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
