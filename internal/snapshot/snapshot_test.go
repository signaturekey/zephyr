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

func TestAcquireWorktreeRejectsEscapingUntrackedSymlink(t *testing.T) {
	repo := newRepository(t)
	writeFile(t, filepath.Join(repo, "main.go"), "package demo\n")
	gitCommand(t, repo, "add", "main.go")
	gitCommand(t, repo, "commit", "-m", "initial")
	require.NoError(t, os.Symlink("../outside", filepath.Join(repo, "escape")))

	_, err := Acquire(context.Background(), Request{Repository: repo, Source: SourceWorktree})
	require.ErrorContains(t, err, "escapes snapshot root")
}

func TestSSHCloneURL(t *testing.T) {
	tests := []struct {
		name       string
		repository string
		port       string
		want       string
	}{
		{
			name:       "GitHub clone URL",
			repository: "https://github.com/org/repository.git",
			port:       "22",
			want:       "ssh://git@github.com/org/repository.git",
		},
		{
			name:       "GitLab clone URL",
			repository: "https://gitlab.com/group/repository.git",
			port:       "22",
			want:       "ssh://git@gitlab.com/group/repository.git",
		},
		{
			name:       "Bitbucket clone URL",
			repository: "https://bitbucket.org/workspace/repository.git",
			port:       "12345",
			want:       "ssh://git@bitbucket.org:12345/workspace/repository.git",
		},
		{name: "local repository", repository: "/tmp/repository", port: "22"},
		{name: "HTTPS URL with credentials", repository: "https://user@example.com/org/repository.git", port: "22"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := sshCloneURL(context.Background(), test.repository, func(_ context.Context, _ string) (string, error) {
				return test.port, nil
			})
			if test.want == "" {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestCloneFallsBackFromHTTPSToSSH(t *testing.T) {
	fakeGitDir := t.TempDir()
	fakeGit := filepath.Join(fakeGitDir, "git")
	fakeSSH := filepath.Join(fakeGitDir, "ssh")
	repository := "https://example.test/org/repository.git"
	sshRepository := "ssh://git@example.test:12345/org/repository.git"
	script := "#!/bin/sh\n" +
		"case \"$6\" in\n" +
		"  \"" + repository + "\") mkdir -p \"$7/.git\"; exit 1 ;;\n" +
		"  \"" + sshRepository + "\") mkdir -p \"$7/.git\"; exit 0 ;;\n" +
		"  *) exit 2 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(fakeGit, []byte(script), 0o700))
	sshScript := "#!/bin/sh\n[ \"$1\" = -G ] && [ \"$2\" = example.test ] || exit 1\nprintf 'port 12345\\n'\n"
	require.NoError(t, os.WriteFile(fakeSSH, []byte(sshScript), 0o700))
	t.Setenv("PATH", fakeGitDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	target, err := os.MkdirTemp("", "zephyr-review-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(target)) })

	require.NoError(t, clone(context.Background(), repository, target))
	_, err = os.Stat(filepath.Join(target, ".git"))
	require.NoError(t, err)
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
