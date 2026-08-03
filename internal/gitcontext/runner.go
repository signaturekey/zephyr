package gitcontext

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrCommandNotAllowed = errors.New("Git command is not in the read-only allowlist")
	ErrGitTimeout        = errors.New("Git command timed out")
	ErrGitOutputTooLarge = errors.New("Git command output exceeded the safety limit")
	ErrUnsafeGitConfig   = errors.New("Git configuration can execute an external content filter")
)

const (
	defaultMaxGitOutputBytes int64 = 64 << 20
	defaultMaxGitErrorBytes  int64 = 2 << 20
	maxGitConfigQueryBytes   int64 = 1 << 20
)

type CommandResult struct {
	Repository string
	Args       []string
	Stdout     []byte
	Stderr     []byte
	ExitCode   int
	Duration   time.Duration
}

type Runner interface {
	Run(ctx context.Context, repository string, args ...string) (CommandResult, error)
}

type CommandError struct {
	Result CommandResult
	Cause  error
}

func (e *CommandError) Error() string {
	stderr := strings.TrimSpace(string(e.Result.Stderr))
	args := strings.Join(sanitizedCommandArgs(e.Result.Args), " ")
	if len(stderr) > 2048 {
		stderr = stderr[:2048] + "..."
	}
	if stderr == "" {
		return fmt.Sprintf("git %s failed with exit code %d", args, e.Result.ExitCode)
	}
	return fmt.Sprintf("git %s failed with exit code %d: %s", args, e.Result.ExitCode, stderr)
}

func (e *CommandError) Unwrap() error { return e.Cause }

type SystemRunner struct {
	Path           string
	Timeout        time.Duration
	Env            []string
	Now            func() time.Time
	MaxOutputBytes int64
}

func NewSystemRunner(timeout time.Duration) *SystemRunner {
	return &SystemRunner{Path: "git", Timeout: timeout, Now: time.Now}
}

func (r *SystemRunner) Run(ctx context.Context, repository string, args ...string) (CommandResult, error) {
	result := CommandResult{
		Repository: repository,
		Args:       append([]string(nil), args...),
		ExitCode:   -1,
	}
	if err := validateReadOnlyCommand(args); err != nil {
		return result, err
	}
	if strings.TrimSpace(repository) == "" {
		return result, errors.New("Git repository path is required")
	}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("run git %s: %w", args[0], err)
	}

	commandContext := ctx
	cancel := func() {}
	if r.Timeout > 0 {
		commandContext, cancel = context.WithTimeout(ctx, r.Timeout)
	}
	defer cancel()

	path := r.Path
	if path == "" {
		path = "git"
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	environment := commandEnvironment(r.Env)
	startedAt := now()
	if commandComparesWorkingTree(args) {
		if err := rejectExternalContentFilters(commandContext, path, repository, environment); err != nil {
			result.Duration = now().Sub(startedAt)
			if contextErr := commandContext.Err(); contextErr != nil {
				if errors.Is(contextErr, context.DeadlineExceeded) && ctx.Err() == nil {
					return result, fmt.Errorf("inspect Git filter configuration: %w: %w", ErrGitTimeout, contextErr)
				}
				return result, fmt.Errorf("inspect Git filter configuration: %w", contextErr)
			}
			return result, fmt.Errorf("inspect Git filter configuration: %w", err)
		}
	}
	actualArgs := make([]string, 0, len(args)+10)
	actualArgs = append(actualArgs, safeGitArguments(repository)...)
	actualArgs = append(actualArgs, args...)
	command := exec.CommandContext(commandContext, path, actualArgs...)
	command.Env = environment
	maximum := r.MaxOutputBytes
	if maximum <= 0 {
		maximum = defaultMaxGitOutputBytes
	}
	stdout := boundedBuffer{maximum: maximum}
	stderr := boundedBuffer{maximum: defaultMaxGitErrorBytes}
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result.Duration = now().Sub(startedAt)
	result.Stdout = append([]byte(nil), stdout.Bytes()...)
	result.Stderr = append([]byte(nil), stderr.Bytes()...)
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if stdout.truncated || stderr.truncated {
		return result, fmt.Errorf("run git %s: %w (stdout limit %d bytes, stderr limit %d bytes)", args[0], ErrGitOutputTooLarge, maximum, defaultMaxGitErrorBytes)
	}
	if err == nil {
		result.ExitCode = 0
		return result, nil
	}
	if contextErr := commandContext.Err(); contextErr != nil {
		if errors.Is(contextErr, context.DeadlineExceeded) && ctx.Err() == nil {
			return result, fmt.Errorf("run git %s: %w: %w", args[0], ErrGitTimeout, contextErr)
		}
		return result, fmt.Errorf("run git %s: %w", args[0], contextErr)
	}
	return result, &CommandError{Result: result, Cause: err}
}

func commandComparesWorkingTree(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "status" {
		return true
	}
	if args[0] != "diff" {
		return false
	}
	objectIDs := 0
	cached := false
	separatorSeen := false
	for _, argument := range args[1:] {
		if argument == "--" {
			separatorSeen = true
			break
		}
		if argument == "--cached" {
			cached = true
			continue
		}
		if isFullObjectID(argument) {
			objectIDs++
			continue
		}
		if !isKnownObjectDiffOption(argument) {
			return true
		}
	}
	if !separatorSeen {
		return true
	}
	if cached {
		return false
	}
	return objectIDs != 2
}

func isKnownObjectDiffOption(value string) bool {
	switch value {
	case "--no-ext-diff", "--no-textconv", "--no-color", "--find-renames",
		"--submodule=short", "--ignore-submodules=dirty", "--raw", "--no-abbrev",
		"-z", "--numstat", "--patch", "--full-index":
		return true
	default:
		return false
	}
}

func isFullObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func safeGitArguments(repository string) []string {
	return []string{
		"--no-pager",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "credential.helper=",
		"-C", repository,
	}
}

func rejectExternalContentFilters(ctx context.Context, path, repository string, environment []string) error {
	args := append(safeGitArguments(repository),
		"config", "--includes", "--null", "--name-only", "--get-regexp", `^filter\.`,
	)
	command := exec.CommandContext(ctx, path, args...)
	command.Env = environment
	stdout := boundedBuffer{maximum: maxGitConfigQueryBytes}
	stderr := boundedBuffer{maximum: defaultMaxGitErrorBytes}
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if stdout.truncated || stderr.truncated {
		return fmt.Errorf("%w: filter configuration query exceeded its output limit", ErrUnsafeGitConfig)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 && len(stdout.Bytes()) == 0 {
			return nil
		}
		return fmt.Errorf("query effective filter configuration: exit code unavailable or non-zero")
	}
	for _, rawKey := range bytes.Split(stdout.Bytes(), []byte{0}) {
		key := strings.TrimSpace(string(rawKey))
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "filter.") &&
			(strings.HasSuffix(lower, ".clean") || strings.HasSuffix(lower, ".process")) {
			return fmt.Errorf("%w: external clean/process filter is configured", ErrUnsafeGitConfig)
		}
	}
	return nil
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	maximum   int64
	truncated bool
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := buffer.maximum - int64(buffer.buffer.Len())
	if remaining > 0 {
		if int64(len(data)) > remaining {
			data = data[:remaining]
			buffer.truncated = true
		}
		_, _ = buffer.buffer.Write(data)
	} else if len(data) > 0 {
		buffer.truncated = true
	}
	return original, nil
}

func (buffer *boundedBuffer) Bytes() []byte { return buffer.buffer.Bytes() }

func commandEnvironment(configured []string) []string {
	environment := configured
	if environment == nil {
		environment = os.Environ()
	} else {
		environment = append([]string(nil), environment...)
	}
	environment = scrubGitEnvironment(environment)
	environment = setEnvironment(environment, "GIT_OPTIONAL_LOCKS", "0")
	environment = setEnvironment(environment, "GIT_PAGER", "cat")
	environment = setEnvironment(environment, "GIT_TERMINAL_PROMPT", "0")
	environment = setEnvironment(environment, "GIT_NO_LAZY_FETCH", "1")
	environment = setEnvironment(environment, "GIT_CONFIG_NOSYSTEM", "1")
	environment = setEnvironment(environment, "GIT_CONFIG_GLOBAL", os.DevNull)
	environment = setEnvironment(environment, "GIT_ATTR_NOSYSTEM", "1")
	environment = setEnvironment(environment, "LC_ALL", "C")
	return environment
}

func scrubGitEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "GIT_") {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func setEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	for i := range environment {
		if strings.HasPrefix(environment[i], prefix) {
			environment[i] = prefix + value
			return environment
		}
	}
	return append(environment, prefix+value)
}

func validateReadOnlyCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: empty command", ErrCommandNotAllowed)
	}
	allowed := false
	switch args[0] {
	case "--version", "rev-parse", "symbolic-ref", "status", "diff", "merge-base", "ls-files":
		allowed = true
	}
	if !allowed {
		return fmt.Errorf("%w: %s", ErrCommandNotAllowed, args[0])
	}
	if args[0] == "symbolic-ref" {
		if len(args) != 4 || args[1] != "--quiet" || args[2] != "--short" || args[3] != "HEAD" {
			return fmt.Errorf("%w: symbolic-ref is restricted to reading HEAD", ErrCommandNotAllowed)
		}
	}
	if args[0] == "ls-files" {
		if len(args) < 3 || args[1] != "-z" || args[2] != "--" {
			return fmt.Errorf("%w: ls-files is restricted to exact tracked path queries", ErrCommandNotAllowed)
		}
		for _, value := range args[3:] {
			if value == "" || filepath.IsAbs(value) || value == ".." || strings.HasPrefix(filepath.Clean(value), ".."+string(filepath.Separator)) {
				return fmt.Errorf("%w: unsafe ls-files path %q", ErrCommandNotAllowed, value)
			}
		}
	}
	if args[0] == "diff" && (!containsArgument(args, "--no-ext-diff") || !containsArgument(args, "--no-textconv")) {
		return fmt.Errorf("%w: diff must explicitly disable external diff and textconv", ErrCommandNotAllowed)
	}
	for _, argument := range args[1:] {
		lower := strings.ToLower(argument)
		if lower == "--ext-diff" || lower == "--textconv" || lower == "--no-index" ||
			strings.HasPrefix(lower, "--output=") || lower == "--output" {
			return fmt.Errorf("%w: unsafe option %s", ErrCommandNotAllowed, argument)
		}
	}
	return nil
}

func containsArgument(args []string, expected string) bool {
	for _, argument := range args {
		if argument == expected {
			return true
		}
	}
	return false
}
