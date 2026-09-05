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
