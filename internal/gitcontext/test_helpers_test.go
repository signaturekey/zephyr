package gitcontext

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testRepository struct {
	path string
	env  []string
}

func newTestRepository(t *testing.T, name string) testRepository {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git executable is unavailable: %v", err)
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	require.NoError(t, os.Mkdir(home, 0o700))
	repository := testRepository{
		path: filepath.Join(base, name),
		env: append(os.Environ(),
			"HOME="+home,
			"XDG_CONFIG_HOME="+filepath.Join(base, "xdg"),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL="+filepath.Join(base, "no-global-config"),
			"LC_ALL=C",
			"TZ=UTC",
		),
	}
	require.NoError(t, os.Mkdir(repository.path, 0o700))
	repository.git(t, "init", "-q")
	repository.git(t, "config", "user.name", "Zephyr Test")
	repository.git(t, "config", "user.email", "zephyr@example.invalid")
	repository.git(t, "config", "commit.gpgsign", "false")
	repository.git(t, "branch", "-m", "main")
	return repository
}

func (repository testRepository) git(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	commandArgs := append([]string{"-C", repository.path}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = repository.env
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
	return string(output)
}

func (repository testRepository) write(t *testing.T, relative string, content []byte) {
	t.Helper()
	path := filepath.Join(repository.path, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, content, 0o600))
}

func (repository testRepository) append(t *testing.T, relative, content string) {
	t.Helper()
	path := filepath.Join(repository.path, filepath.FromSlash(relative))
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	defer file.Close()
	_, err = file.WriteString(content)
	require.NoError(t, err)
}

func (repository testRepository) commitAll(t *testing.T, message string) string {
	t.Helper()
	repository.git(t, "add", "--all", "--")
	repository.git(t, "commit", "-q", "-m", message)
	return repository.git(t, "rev-parse", "HEAD")[:40]
}

func (repository testRepository) collector(t *testing.T) *Collector {
	t.Helper()
	collector, err := NewCollector(&SystemRunner{
		Path:    "git",
		Timeout: 5 * time.Second,
		Env:     repository.env,
	})
	require.NoError(t, err)
	return collector
}

func findChange(t *testing.T, snapshot Snapshot, path string) FileChange {
	t.Helper()
	for _, change := range snapshot.Changes {
		if change.Path == path {
			return change
		}
	}
	t.Fatalf("change %q not found in %#v", path, snapshot.Changes)
	return FileChange{}
}

func findUntracked(t *testing.T, snapshot Snapshot, path string) UntrackedFile {
	t.Helper()
	for _, file := range snapshot.Status.Untracked {
		if file.Path == path {
			return file
		}
	}
	t.Fatalf("untracked file %q not found in %#v", path, snapshot.Status.Untracked)
	return UntrackedFile{}
}

func assertSortedChanges(t *testing.T, changes []FileChange) {
	t.Helper()
	for i := 1; i < len(changes); i++ {
		if changes[i-1].Path > changes[i].Path {
			t.Fatalf("changes are not sorted: %q before %q", changes[i-1].Path, changes[i].Path)
		}
	}
}
