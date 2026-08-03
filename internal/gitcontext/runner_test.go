package gitcontext

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSystemRunnerRejectsWriteCommandsAndUnsafeDiffOptions(t *testing.T) {
	t.Parallel()
	runner := NewSystemRunner(time.Second)
	for _, args := range [][]string{
		{"commit", "-m", "forbidden"},
		{"push"},
		{"diff", "--output=result.patch"},
		{"diff", "--ext-diff"},
		{"diff", "--no-index", "left", "right"},
		{"diff", "--patch"},
		{"symbolic-ref", "HEAD", "refs/heads/forbidden"},
	} {
		if _, err := runner.Run(context.Background(), t.TempDir(), args...); !errors.Is(err, ErrCommandNotAllowed) {
			t.Errorf("Run(%v) error = %v, want ErrCommandNotAllowed", args, err)
		}
	}
}

func TestCommandComparesWorkingTree(t *testing.T) {
	t.Parallel()
	shaA := strings.Repeat("a", 40)
	shaB := strings.Repeat("b", 40)
	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "status", args: []string{"status", "--porcelain=v2"}, want: true},
		{name: "working tree", args: []string{"diff", "--no-ext-diff", "--no-textconv", "--"}, want: true},
		{name: "branch to worktree", args: []string{"diff", "--no-ext-diff", "--no-textconv", shaA, "--"}, want: true},
		{name: "staged", args: []string{"diff", "--no-ext-diff", "--no-textconv", "--cached", shaA, "--"}, want: false},
		{name: "commit range", args: []string{"diff", "--no-ext-diff", "--no-textconv", shaA, shaB, "--"}, want: false},
		{name: "symbolic refs fail closed", args: []string{"diff", "--no-ext-diff", "--no-textconv", "HEAD", "main", "--"}, want: true},
		{name: "cached path fails closed", args: []string{"diff", "--no-ext-diff", "--no-textconv", "--", "--cached"}, want: true},
		{name: "cached-looking option value fails closed", args: []string{"diff", "--no-ext-diff", "--no-textconv", "-S", "--cached", "--"}, want: true},
		{name: "unknown option after cached fails closed", args: []string{"diff", "--no-ext-diff", "--no-textconv", "--cached", "--word-diff-regex", "value", shaA, "--"}, want: true},
		{name: "unknown option fails closed", args: []string{"diff", "--no-ext-diff", "--no-textconv", "--word-diff-regex", shaA, shaB, "--"}, want: true},
		{name: "missing separator fails closed", args: []string{"diff", "--no-ext-diff", "--no-textconv", shaA, shaB}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := commandComparesWorkingTree(test.args); got != test.want {
				t.Fatalf("commandComparesWorkingTree(%v) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}

func TestSystemRunnerScrubsHostileGitEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper is Unix-specific")
	}
	directory := t.TempDir()
	script := filepath.Join(directory, "env-git")
	content := "#!/bin/sh\nenv | LC_ALL=C sort\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &SystemRunner{Path: script, Timeout: 5 * time.Second, Env: []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_DIR=/tmp/redirected",
		"GIT_WORK_TREE=/tmp/redirected-tree",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.fsmonitor",
		"GIT_CONFIG_VALUE_0=/tmp/hostile-hook",
		"GIT_TRACE=/tmp/trace-leak",
		"GIT_LITERAL_PATHSPECS=1",
	}}
	result, err := runner.Run(context.Background(), directory, "status", "--porcelain=v2")
	if err != nil {
		t.Fatal(err)
	}
	environment := string(result.Stdout)
	for _, forbidden := range []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_CONFIG_COUNT=", "GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_", "GIT_TRACE=", "GIT_LITERAL_PATHSPECS="} {
		if strings.Contains(environment, forbidden) {
			t.Fatalf("hostile variable %q survived:\n%s", forbidden, environment)
		}
	}
	for _, expected := range []string{"GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_CONFIG_NOSYSTEM=1"} {
		if !strings.Contains(environment, expected) {
			t.Fatalf("safe variable %q is missing:\n%s", expected, environment)
		}
	}
}

func TestSystemRunnerRejectsCachedLookingOptionValueBeforeFilterExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable filter fixture is Unix-specific")
	}
	repository := newTestRepository(t, "cached-looking-option")
	repository.write(t, ".gitattributes", []byte("*.txt filter=zephyraudit\n"))
	repository.write(t, "base.txt", []byte("base\n"))
	repository.commitAll(t, "base with inert filter attribute")

	marker := filepath.Join(t.TempDir(), "filter-ran")
	filter := filepath.Join(t.TempDir(), "clean-filter")
	if err := os.WriteFile(filter, []byte("#!/bin/sh\ntouch \""+marker+"\"\ncat\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	repository.git(t, "config", "filter.zephyraudit.clean", filter)
	repository.write(t, "base.txt", []byte("changed\n"))

	runner := &SystemRunner{Path: "git", Timeout: 5 * time.Second, Env: repository.env}
	_, err := runner.Run(
		context.Background(),
		repository.path,
		"diff", "--no-ext-diff", "--no-textconv", "-S", "--cached", "--",
	)
	if !errors.Is(err, ErrUnsafeGitConfig) {
		t.Fatalf("runner error = %v, want ErrUnsafeGitConfig", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("external clean filter executed; marker stat error = %v", statErr)
	}
}

func TestSystemRunnerBoundsGitOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper is Unix-specific")
	}
	directory := t.TempDir()
	script := filepath.Join(directory, "large-git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '1234567890'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &SystemRunner{Path: script, Timeout: 5 * time.Second, MaxOutputBytes: 4}
	result, err := runner.Run(context.Background(), directory, "status", "--porcelain=v2")
	if !errors.Is(err, ErrGitOutputTooLarge) {
		t.Fatalf("output error = %v, want ErrGitOutputTooLarge", err)
	}
	if string(result.Stdout) != "1234" {
		t.Fatalf("bounded stdout = %q", result.Stdout)
	}
}

func TestSystemRunnerReturnsTypedCommandFailure(t *testing.T) {
	t.Parallel()
	repository := newTestRepository(t, "failure")
	repository.write(t, "base.txt", []byte("base\n"))
	repository.commitAll(t, "base")
	runner := &SystemRunner{Path: "git", Timeout: 5 * time.Second, Env: repository.env}
	result, err := runner.Run(context.Background(), repository.path, "rev-parse", "--verify", "missing-ref")
	if err == nil {
		t.Fatal("Run() unexpectedly succeeded")
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("Run() error = %T %v, want *CommandError", err, err)
	}
	if result.ExitCode == 0 || commandErr.Result.ExitCode != result.ExitCode || len(result.Stderr) == 0 {
		t.Fatalf("command result did not preserve failure metadata: %#v", result)
	}
}

func TestSystemRunnerTimeoutAndCancellation(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell helper is Unix-specific")
	}
	directory := t.TempDir()
	script := filepath.Join(directory, "slow-git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &SystemRunner{Path: script, Timeout: 20 * time.Millisecond}
	_, err := runner.Run(context.Background(), directory, "status", "--porcelain=v2")
	if !errors.Is(err, ErrGitTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v, want ErrGitTimeout and DeadlineExceeded", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runner.Run(ctx, directory, "status", "--porcelain=v2")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v, want context.Canceled", err)
	}
}
