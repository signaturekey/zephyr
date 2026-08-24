package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type Source string

const (
	SourceWorktree Source = "worktree"
	SourceCommit   Source = "commit"
	SourceBranch   Source = "branch"
)

const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

type Request struct {
	Repository string
	Source     Source
	Commit     string
	Branch     string
	Base       string
	KeepTemp   bool
}

type Snapshot struct {
	Root         string   `json:"-"`
	Source       Source   `json:"source"`
	Repository   string   `json:"repository"`
	Branch       string   `json:"branch,omitempty"`
	HeadSHA      string   `json:"head_sha"`
	BaseRef      string   `json:"base_ref,omitempty"`
	BaseSHA      string   `json:"base_sha"`
	MergeBase    string   `json:"merge_base,omitempty"`
	Diff         string   `json:"diff"`
	ChangedPaths []string `json:"changed_paths"`
	Untracked    []string `json:"untracked,omitempty"`
	KeepTemp     bool     `json:"-"`
}

func Acquire(ctx context.Context, request Request) (*Snapshot, error) {
	if strings.TrimSpace(request.Repository) == "" {
		request.Repository = "."
	}
	if request.Source == "" {
		request.Source = SourceWorktree
	}
	root, err := os.MkdirTemp("", "zephyr-review-")
	if err != nil {
		return nil, fmt.Errorf("create snapshot directory: %w", err)
	}
	snapshot := &Snapshot{Root: root, Source: request.Source, Repository: request.Repository, KeepTemp: request.KeepTemp}
	cleanup := func(err error) (*Snapshot, error) {
		if !request.KeepTemp {
			_ = os.RemoveAll(root)
		}
		return nil, err
	}

	switch request.Source {
	case SourceWorktree:
		if err := acquireWorktree(ctx, snapshot, request.Repository); err != nil {
			return cleanup(err)
		}
	case SourceCommit:
		if strings.TrimSpace(request.Commit) == "" {
			return cleanup(errors.New("commit source requires a commit"))
		}
		if err := acquireCommit(ctx, snapshot, request.Repository, request.Commit); err != nil {
			return cleanup(err)
		}
	case SourceBranch:
		if strings.TrimSpace(request.Branch) == "" || strings.TrimSpace(request.Base) == "" {
			return cleanup(errors.New("branch source requires branch and base refs"))
		}
		if err := acquireBranch(ctx, snapshot, request.Repository, request.Branch, request.Base); err != nil {
			return cleanup(err)
		}
	default:
		return cleanup(fmt.Errorf("unsupported snapshot source %q", request.Source))
	}
	return snapshot, nil
}

func (snapshot *Snapshot) Cleanup() error {
	if snapshot == nil || snapshot.KeepTemp || snapshot.Root == "" {
		return nil
	}
	base := filepath.Base(snapshot.Root)
	rootParent, rootErr := filepath.EvalSymlinks(filepath.Dir(snapshot.Root))
	tempRoot, tempErr := filepath.EvalSymlinks(os.TempDir())
	if !strings.HasPrefix(base, "zephyr-review-") || rootErr != nil || tempErr != nil || rootParent != tempRoot {
		return fmt.Errorf("refuse to remove unowned snapshot root %q", snapshot.Root)
	}
	return os.RemoveAll(snapshot.Root)
}

func acquireWorktree(ctx context.Context, snapshot *Snapshot, repository string) error {
	repoRoot, err := gitText(ctx, "", "-C", repository, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("resolve repository: %w", err)
	}
	repoRoot = strings.TrimSpace(repoRoot)
	snapshot.Repository = repoRoot
	snapshot.HeadSHA, err = gitText(ctx, "", "-C", repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve HEAD: %w", err)
	}
	snapshot.HeadSHA = strings.TrimSpace(snapshot.HeadSHA)
	snapshot.BaseSHA = snapshot.HeadSHA
	snapshot.Branch, _ = gitText(ctx, "", "-C", repoRoot, "branch", "--show-current")
	snapshot.Branch = strings.TrimSpace(snapshot.Branch)

	if err := clone(ctx, repoRoot, snapshot.Root); err != nil {
		return err
	}
	if _, err := git(ctx, nil, "-C", snapshot.Root, "checkout", "--quiet", "--detach", snapshot.HeadSHA); err != nil {
		return fmt.Errorf("checkout snapshot HEAD: %w", err)
	}
	trackedDiff, err := git(ctx, nil, "-C", repoRoot, "diff", "--binary", "--find-renames", "HEAD", "--")
	if err != nil {
		return fmt.Errorf("collect worktree diff: %w", err)
	}
	if len(trackedDiff) != 0 {
		if _, err := git(ctx, trackedDiff, "-C", snapshot.Root, "apply", "--binary", "--whitespace=nowarn", "-"); err != nil {
			return fmt.Errorf("apply worktree diff to snapshot: %w", err)
		}
	}

	untrackedRaw, err := git(ctx, nil, "-C", repoRoot, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return fmt.Errorf("list untracked files: %w", err)
	}
	snapshot.Untracked = splitNUL(untrackedRaw)
	for _, relative := range snapshot.Untracked {
		if err := copyUntracked(repoRoot, snapshot.Root, relative); err != nil {
			return err
		}
	}

	var diff bytes.Buffer
	diff.Write(trackedDiff)
	for _, relative := range snapshot.Untracked {
		part, code, err := gitExit(ctx, nil, "-C", snapshot.Root, "diff", "--no-index", "--binary", "--", "/dev/null", relative)
		if code != 0 && code != 1 {
			return fmt.Errorf("build untracked diff for %q: %w", relative, err)
		}
		if diff.Len() > 0 && diff.Bytes()[diff.Len()-1] != '\n' {
			diff.WriteByte('\n')
		}
		diff.Write(part)
	}
	snapshot.Diff = diff.String()
	pathsRaw, err := git(ctx, nil, "-C", repoRoot, "diff", "--name-only", "-z", "HEAD", "--")
	if err != nil {
		return fmt.Errorf("collect changed paths: %w", err)
	}
	snapshot.ChangedPaths = uniqueSorted(append(splitNUL(pathsRaw), snapshot.Untracked...))
	return nil
}

func acquireCommit(ctx context.Context, snapshot *Snapshot, repository, commit string) error {
	if err := clone(ctx, repository, snapshot.Root); err != nil {
		return err
	}
	head, err := resolveCommit(ctx, snapshot.Root, commit)
	if err != nil {
		if _, fetchErr := git(ctx, nil, "-C", snapshot.Root, "fetch", "--quiet", "origin", commit); fetchErr != nil {
			return fmt.Errorf("resolve commit %q: %w", commit, err)
		}
		head, err = resolveCommit(ctx, snapshot.Root, commit)
		if err != nil {
			return fmt.Errorf("resolve fetched commit %q: %w", commit, err)
		}
	}
	if _, err := git(ctx, nil, "-C", snapshot.Root, "checkout", "--quiet", "--detach", head); err != nil {
		return fmt.Errorf("checkout commit %s: %w", head, err)
	}
	parents, err := gitText(ctx, "", "-C", snapshot.Root, "rev-list", "--parents", "-n", "1", head)
	if err != nil {
		return fmt.Errorf("resolve commit parent: %w", err)
	}
	fields := strings.Fields(parents)
	base := emptyTreeSHA
	if len(fields) > 1 {
		base = fields[1]
	}
	diff, err := git(ctx, nil, "-C", snapshot.Root, "diff", "--binary", "--find-renames", base, head, "--")
	if err != nil {
		return fmt.Errorf("build commit diff: %w", err)
	}
	paths, err := git(ctx, nil, "-C", snapshot.Root, "diff", "--name-only", "-z", base, head, "--")
	if err != nil {
		return fmt.Errorf("list commit paths: %w", err)
	}
	snapshot.HeadSHA = head
	snapshot.BaseSHA = base
	snapshot.Diff = string(diff)
	snapshot.ChangedPaths = uniqueSorted(splitNUL(paths))
	return nil
}

func acquireBranch(ctx context.Context, snapshot *Snapshot, repository, branch, base string) error {
	if err := clone(ctx, repository, snapshot.Root); err != nil {
		return err
	}
	head, err := resolveRef(ctx, snapshot.Root, branch)
	if err != nil {
		return fmt.Errorf("resolve branch %q: %w", branch, err)
	}
	baseSHA, err := resolveRef(ctx, snapshot.Root, base)
	if err != nil {
		return fmt.Errorf("resolve base %q: %w", base, err)
	}
	mergeBase, err := gitText(ctx, "", "-C", snapshot.Root, "merge-base", baseSHA, head)
	if err != nil {
		return fmt.Errorf("find merge base: %w", err)
	}
	mergeBase = strings.TrimSpace(mergeBase)
	if _, err := git(ctx, nil, "-C", snapshot.Root, "checkout", "--quiet", "--detach", head); err != nil {
		return fmt.Errorf("checkout branch head: %w", err)
	}
	diff, err := git(ctx, nil, "-C", snapshot.Root, "diff", "--binary", "--find-renames", mergeBase, head, "--")
	if err != nil {
		return fmt.Errorf("build branch diff: %w", err)
	}
	paths, err := git(ctx, nil, "-C", snapshot.Root, "diff", "--name-only", "-z", mergeBase, head, "--")
	if err != nil {
		return fmt.Errorf("list branch paths: %w", err)
	}
	snapshot.Branch = branch
	snapshot.BaseRef = base
	snapshot.HeadSHA = head
	snapshot.BaseSHA = baseSHA
	snapshot.MergeBase = mergeBase
	snapshot.Diff = string(diff)
	snapshot.ChangedPaths = uniqueSorted(splitNUL(paths))
	return nil
}

func clone(ctx context.Context, repository, target string) error {
	if _, err := git(ctx, nil, "clone", "--quiet", "--no-checkout", "--no-recurse-submodules", "--", repository, target); err != nil {
		return fmt.Errorf("clone %q: %w", repository, err)
	}
	return nil
}

func resolveCommit(ctx context.Context, root, value string) (string, error) {
	resolved, err := gitText(ctx, "", "-C", root, "rev-parse", "--verify", value+"^{commit}")
	return strings.TrimSpace(resolved), err
}

func resolveRef(ctx context.Context, root, value string) (string, error) {
	for _, candidate := range []string{value, "origin/" + strings.TrimPrefix(value, "origin/")} {
		if resolved, err := resolveCommit(ctx, root, candidate); err == nil {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("ref %q is not present in clone", value)
}

func copyUntracked(sourceRoot, targetRoot, relative string) error {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("untracked path escapes repository: %q", relative)
	}
	source := filepath.Join(sourceRoot, clean)
	target := filepath.Join(targetRoot, clean)
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect untracked %q: %w", relative, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("create untracked parent %q: %w", relative, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(source)
		if err != nil {
			return fmt.Errorf("read untracked symlink %q: %w", relative, err)
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(clean), link))
		if filepath.IsAbs(link) || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
			return fmt.Errorf("untracked symlink %q escapes snapshot root", relative)
		}
		if err := os.Symlink(link, target); err != nil {
			return fmt.Errorf("copy untracked symlink %q: %w", relative, err)
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("untracked path %q is not a regular file or symlink", relative)
	}
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open untracked %q: %w", relative, err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create untracked snapshot %q: %w", relative, err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("copy untracked %q: %w", relative, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close untracked %q: %w", relative, closeErr)
	}
	return nil
}

func splitNUL(data []byte) []string {
	parts := bytes.Split(data, []byte{0})
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) != 0 {
			values = append(values, filepath.ToSlash(string(part)))
		}
	}
	return values
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[filepath.ToSlash(value)] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func gitText(ctx context.Context, input string, args ...string) (string, error) {
	var data []byte
	if input != "" {
		data = []byte(input)
	}
	output, err := git(ctx, data, args...)
	return string(output), err
}

func git(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	output, code, err := gitExit(ctx, input, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("git %s exited with %d", strings.Join(args, " "), code)
	}
	return output, nil
}

func gitExit(ctx context.Context, input []byte, args ...string) ([]byte, int, error) {
	command := exec.CommandContext(ctx, "git", args...)
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return stdout.Bytes(), 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.Bytes(), exit.ExitCode(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), -1, fmt.Errorf("run git: %w", err)
}
