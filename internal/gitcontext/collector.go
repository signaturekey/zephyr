package gitcontext

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signaturekey/zephyr/internal/run"
)

var ErrRepositoryChangedDuringCollection = errors.New("Git repository changed while the snapshot was being collected")

const defaultMaxUntrackedBytes int64 = 256 * 1024

type Collector struct {
	runner Runner
	now    func() time.Time
}

func (c *Collector) TrackedPaths(ctx context.Context, repository string, paths []string) (map[string]bool, error) {
	result := make(map[string]bool)
	if len(paths) == 0 {
		return result, nil
	}
	args := []string{"ls-files", "-z", "--"}
	for _, value := range paths {
		if _, err := repositoryPath(repository, value); err != nil {
			return nil, err
		}
		args = append(args, filepath.ToSlash(value))
	}
	command, err := c.runner.Run(ctx, repository, args...)
	if err != nil {
		return nil, fmt.Errorf("list tracked instruction paths: %w", err)
	}
	for _, value := range strings.Split(string(command.Stdout), "\x00") {
		if value != "" {
			result[filepath.ToSlash(value)] = true
		}
	}
	return result, nil
}

func NewCollector(runner Runner) (*Collector, error) {
	if runner == nil {
		return nil, errors.New("Git runner is required")
	}
	return &Collector{runner: runner, now: time.Now}, nil
}

func (c *Collector) Collect(ctx context.Context, options Options) (Snapshot, error) {
	if err := validateOptions(options); err != nil {
		return Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, fmt.Errorf("collect Git snapshot: %w", err)
	}
	repository, err := filepath.Abs(options.Repository)
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve repository %q: %w", options.Repository, err)
	}
	repository = filepath.Clean(repository)
	commands := make([]CommandMetadata, 0, 24)

	rootResult, err := c.execute(ctx, repository, &commands, "rev-parse", "--show-toplevel")
	if err != nil {
		return Snapshot{}, fmt.Errorf("resolve Git repository root from %q: %w", repository, err)
	}
	root := filepath.Clean(strings.TrimSpace(string(rootResult.Stdout)))
	if root == "" {
		return Snapshot{}, fmt.Errorf("resolve Git repository root from %q: Git returned an empty path", repository)
	}
	if !filepath.IsAbs(root) {
		root, err = filepath.Abs(filepath.Join(repository, root))
		if err != nil {
			return Snapshot{}, fmt.Errorf("resolve absolute Git repository root %q: %w", root, err)
		}
	}

	versionResult, err := c.execute(ctx, root, &commands, "--version")
	if err != nil {
		return Snapshot{}, fmt.Errorf("read Git version: %w", err)
	}
	gitVersion := strings.TrimSpace(string(versionResult.Stdout))

	branch := ""
	detached := false
	branchResult, branchErr := c.execute(ctx, root, &commands, "symbolic-ref", "--quiet", "--short", "HEAD")
	if branchErr != nil {
		if !isExitCode(branchErr, 1) {
			return Snapshot{}, fmt.Errorf("read current Git branch: %w", branchErr)
		}
		detached = true
	} else {
		branch = strings.TrimSpace(string(branchResult.Stdout))
	}

	headUnavailable := false
	headSHA, headErr := c.resolveCommit(ctx, root, &commands, "HEAD")
	if headErr != nil {
		if options.Source != run.SourcePlanOnly || !isCommandFailure(headErr) {
			return Snapshot{}, fmt.Errorf("resolve HEAD: %w", headErr)
		}
		headSHA = ""
		headUnavailable = true
	}

	worktreeAware := options.Source == run.SourceWorkingTree || options.Source == run.SourceBranch
	initialState := workingState{status: WorktreeStatus{Entries: []StatusEntry{}, Untracked: []UntrackedFile{}}}
	if worktreeAware {
		initialState, err = c.captureWorkingState(ctx, root, headSHA, options.RestrictedPatterns, &commands)
		if err != nil {
			return Snapshot{}, fmt.Errorf("capture initial working-tree state: %w", err)
		}
	}

	repositoryMetadata := RepositoryMetadata{
		Root:        root,
		GitVersion:  gitVersion,
		Branch:      branch,
		Detached:    detached,
		HeadSHA:     headSHA,
		BaseRef:     strings.TrimSpace(options.BaseRef),
		CommitRange: strings.TrimSpace(options.CommitRange),
	}
	scope, err := c.resolveScope(ctx, options, root, headSHA, &repositoryMetadata, &commands)
	if err != nil {
		return Snapshot{}, err
	}

	sourceData, err := c.collectSource(ctx, root, scope, options, &commands)
	if err != nil {
		return Snapshot{}, err
	}
	stagedSourceFingerprint := ""
	if options.Source == run.SourceStaged {
		stagedSourceFingerprint = sourceCollectionFingerprint(sourceData)
	}
	patches := Patches{Full: string(sourceData.patch)}
	switch options.Source {
	case run.SourceWorkingTree, run.SourceBranch:
		stagedScope := diffScope{cached: true}
		if headSHA != "" {
			stagedScope.revisions = []string{headSHA}
		}
		pathspecs := patchPathspecs(sourceData.changes, options.IncludeGenerated, options.IncludeVendor, options.RestrictedPatterns)
		staged, commandErr := c.diff(ctx, root, &commands, stagedScope, []string{"--patch", "--full-index"}, pathspecs)
		if commandErr != nil {
			return Snapshot{}, fmt.Errorf("collect staged patch: %w", commandErr)
		}
		unstaged, commandErr := c.diff(ctx, root, &commands, diffScope{}, []string{"--patch", "--full-index"}, pathspecs)
		if commandErr != nil {
			return Snapshot{}, fmt.Errorf("collect unstaged patch: %w", commandErr)
		}
		patches.Staged = string(staged)
		patches.Unstaged = string(unstaged)
	case run.SourceStaged:
		patches.Staged = patches.Full
	}

	status := initialState.status
	limitations := make([]string, 0)
	if headUnavailable {
		limitations = append(limitations, "repository has no HEAD commit")
	}
	if options.IncludeUntrackedContent && (options.Source == run.SourceWorkingTree || options.Source == run.SourceBranch) {
		maximum := options.MaxUntrackedBytes
		if maximum <= 0 {
			maximum = defaultMaxUntrackedBytes
		}
		untrackedPatch, untrackedLimitations, includeErr := collectUntracked(root, &status, options, maximum)
		if includeErr != nil {
			return Snapshot{}, fmt.Errorf("collect explicitly included untracked content: %w", includeErr)
		}
		if untrackedPatch != "" {
			patches.Full = joinPatches(patches.Full, untrackedPatch)
			patches.Unstaged = joinPatches(patches.Unstaged, untrackedPatch)
		}
		limitations = append(limitations, untrackedLimitations...)
	}

	finalFingerprint := initialState.fingerprint
	if worktreeAware {
		finalState, captureErr := c.captureWorkingState(ctx, root, headSHA, options.RestrictedPatterns, &commands)
		if captureErr != nil {
			return Snapshot{}, fmt.Errorf("capture final working-tree state: %w", captureErr)
		}
		finalFingerprint = finalState.fingerprint
	}
	stagedSourceChanged := false
	if options.Source == run.SourceStaged {
		finalSource, collectErr := c.collectSource(ctx, root, scope, options, &commands)
		if collectErr != nil {
			return Snapshot{}, fmt.Errorf("recheck staged source after collection: %w", collectErr)
		}
		stagedSourceChanged = stagedSourceFingerprint != sourceCollectionFingerprint(finalSource)
	}
	endHead := ""
	if headSHA != "" {
		endHead, err = c.resolveCommit(ctx, root, &commands, "HEAD")
		if err != nil {
			return Snapshot{}, fmt.Errorf("recheck HEAD after collection: %w", err)
		}
	}
	if headSHA != endHead || initialState.fingerprint != finalFingerprint || stagedSourceChanged {
		return Snapshot{}, ErrRepositoryChangedDuringCollection
	}

	repositoryMetadata.Commands = commands
	sourceFingerprint := fingerprint(
		[]byte(options.Source),
		[]byte(repositoryMetadata.BaseSHA),
		[]byte(repositoryMetadata.TargetSHA),
		[]byte(repositoryMetadata.MergeBaseSHA),
		sourceData.raw,
		sourceData.numstat,
		[]byte(patches.Full),
		initialState.statMetadata,
	)
	stats := calculateStats(sourceData.changes)
	stats.Untracked = len(status.Untracked)
	for _, untracked := range status.Untracked {
		if untracked.ContentIncluded {
			stats.ReviewableUntracked++
		}
	}
	return Snapshot{
		Version:                 SnapshotVersion,
		CollectedAt:             c.now().UTC(),
		Source:                  options.Source,
		Repository:              repositoryMetadata,
		Status:                  status,
		Changes:                 sourceData.changes,
		Stats:                   stats,
		Patches:                 patches,
		SourceFingerprint:       sourceFingerprint,
		WorkingTreeFingerprint:  initialState.fingerprint,
		IncludeGenerated:        options.IncludeGenerated,
		IncludeVendor:           options.IncludeVendor,
		IncludeUntrackedContent: options.IncludeUntrackedContent,
		MaxUntrackedBytes:       options.MaxUntrackedBytes,
		RestrictedPatterns:      append([]string(nil), options.RestrictedPatterns...),
		Limitations:             limitations,
	}, nil
}

func validateOptions(options Options) error {
	if strings.TrimSpace(options.Repository) == "" {
		return errors.New("collect Git snapshot: repository is required")
	}
	if err := options.Source.Validate(); err != nil {
		return fmt.Errorf("collect Git snapshot: %w", err)
	}
	if options.Source == run.SourceBranch && strings.TrimSpace(options.BaseRef) == "" {
		return errors.New("collect Git snapshot: branch source requires a base ref")
	}
	if options.Source == run.SourceCommitRange && strings.TrimSpace(options.CommitRange) == "" {
		return errors.New("collect Git snapshot: commit-range source requires a range")
	}
	if options.MaxUntrackedBytes < 0 {
		return errors.New("collect Git snapshot: max untracked bytes cannot be negative")
	}
	if options.MaxUntrackedBytes > maximumAllowedUntrackedBytes {
		return fmt.Errorf("collect Git snapshot: max untracked bytes cannot exceed %d", maximumAllowedUntrackedBytes)
	}
	for index, pattern := range options.RestrictedPatterns {
		if err := validateRestrictedPattern(pattern); err != nil {
			return fmt.Errorf("collect Git snapshot: restricted pattern %d: %w", index, err)
		}
	}
	if options.IncludeUntrackedContent && options.Source != run.SourceWorkingTree && options.Source != run.SourceBranch {
		return fmt.Errorf("collect Git snapshot: untracked content is not part of %s scope", options.Source)
	}
	return nil
}

type diffScope struct {
	cached    bool
	revisions []string
}

type sourceCollection struct {
	raw     []byte
	numstat []byte
	patch   []byte
	changes []FileChange
}

func sourceCollectionFingerprint(source sourceCollection) string {
	return fingerprint(source.raw, source.numstat, source.patch)
}

func (c *Collector) resolveScope(
	ctx context.Context,
	options Options,
	repository string,
	headSHA string,
	metadata *RepositoryMetadata,
	commands *[]CommandMetadata,
) (diffScope, error) {
	switch options.Source {
	case run.SourcePlanOnly:
		metadata.BaseRef = ""
		return diffScope{}, nil
	case run.SourceWorkingTree, run.SourceStaged:
		if headSHA == "" {
			return diffScope{}, errors.New("collect Git snapshot: selected source requires an existing HEAD commit")
		}
		metadata.BaseRef = "HEAD"
		metadata.BaseSHA = headSHA
		metadata.TargetSHA = headSHA
		metadata.MergeBaseSHA = headSHA
		return diffScope{cached: options.Source == run.SourceStaged, revisions: []string{headSHA}}, nil
	case run.SourceBranch:
		if headSHA == "" {
			return diffScope{}, errors.New("collect Git snapshot: branch source requires an existing HEAD commit")
		}
		baseSHA, err := c.resolveCommit(ctx, repository, commands, options.BaseRef)
		if err != nil {
			return diffScope{}, fmt.Errorf("resolve base ref %q: %w", options.BaseRef, err)
		}
		mergeBase, err := c.mergeBase(ctx, repository, commands, baseSHA, headSHA)
		if err != nil {
			return diffScope{}, fmt.Errorf("find merge-base for %q and HEAD: %w", options.BaseRef, err)
		}
		metadata.BaseSHA = baseSHA
		metadata.TargetSHA = headSHA
		metadata.MergeBaseSHA = mergeBase
		return diffScope{revisions: []string{mergeBase}}, nil
	case run.SourceCommitRange:
		fromRef, toRef, threeDot, err := parseCommitRange(options.CommitRange)
		if err != nil {
			return diffScope{}, fmt.Errorf("collect Git snapshot: %w", err)
		}
		fromSHA, err := c.resolveCommit(ctx, repository, commands, fromRef)
		if err != nil {
			return diffScope{}, fmt.Errorf("resolve commit-range start %q: %w", fromRef, err)
		}
		toSHA, err := c.resolveCommit(ctx, repository, commands, toRef)
		if err != nil {
			return diffScope{}, fmt.Errorf("resolve commit-range end %q: %w", toRef, err)
		}
		mergeBase, err := c.mergeBase(ctx, repository, commands, fromSHA, toSHA)
		if err != nil {
			return diffScope{}, fmt.Errorf("find commit-range merge-base: %w", err)
		}
		metadata.BaseRef = fromRef
		metadata.BaseSHA = fromSHA
		metadata.TargetSHA = toSHA
		metadata.MergeBaseSHA = mergeBase
		if threeDot {
			return diffScope{revisions: []string{mergeBase, toSHA}}, nil
		}
		return diffScope{revisions: []string{fromSHA, toSHA}}, nil
	default:
		return diffScope{}, fmt.Errorf("collect Git snapshot: unsupported source %q", options.Source)
	}
}

func (c *Collector) collectSource(
	ctx context.Context,
	repository string,
	scope diffScope,
	options Options,
	commands *[]CommandMetadata,
) (sourceCollection, error) {
	if options.Source == run.SourcePlanOnly {
		return sourceCollection{changes: []FileChange{}}, nil
	}
	raw, err := c.diff(ctx, repository, commands, scope, []string{"--raw", "--no-abbrev", "-z"}, nil)
	if err != nil {
		return sourceCollection{}, fmt.Errorf("collect %s raw diff metadata: %w", options.Source, err)
	}
	changes, err := parseRawChanges(raw)
	if err != nil {
		return sourceCollection{}, fmt.Errorf("parse %s raw diff metadata: %w", options.Source, err)
	}
	changes = applyRestrictedPatterns(changes, options.RestrictedPatterns)
	numstatData, err := c.diff(ctx, repository, commands, scope, []string{"--numstat", "-z"}, nil)
	if err != nil {
		return sourceCollection{}, fmt.Errorf("collect %s diff statistics: %w", options.Source, err)
	}
	numstats, err := parseNumstat(numstatData)
	if err != nil {
		return sourceCollection{}, fmt.Errorf("parse %s diff statistics: %w", options.Source, err)
	}
	changes = attachNumstat(changes, numstats)
	changes = applyContentPolicy(changes, options.IncludeGenerated, options.IncludeVendor)
	patch, err := c.diff(
		ctx,
		repository,
		commands,
		scope,
		[]string{"--patch", "--full-index"},
		patchPathspecs(changes, options.IncludeGenerated, options.IncludeVendor, options.RestrictedPatterns),
	)
	if err != nil {
		return sourceCollection{}, fmt.Errorf("collect %s patch: %w", options.Source, err)
	}
	return sourceCollection{raw: raw, numstat: numstatData, patch: patch, changes: changes}, nil
}

func (c *Collector) diff(
	ctx context.Context,
	repository string,
	commands *[]CommandMetadata,
	scope diffScope,
	outputOptions []string,
	pathspecs []string,
) ([]byte, error) {
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--find-renames", "--submodule=short", "--ignore-submodules=dirty"}
	args = append(args, outputOptions...)
	if scope.cached {
		args = append(args, "--cached")
	}
	args = append(args, scope.revisions...)
	args = append(args, "--")
	args = append(args, pathspecs...)
	result, err := c.execute(ctx, repository, commands, args...)
	if err != nil {
		return nil, err
	}
	return result.Stdout, nil
}

type workingState struct {
	status       WorktreeStatus
	fingerprint  string
	statMetadata []byte
}

func (c *Collector) captureWorkingState(
	ctx context.Context,
	repository string,
	headSHA string,
	restrictedPatterns []string,
	commands *[]CommandMetadata,
) (workingState, error) {
	statusResult, err := c.execute(ctx, repository, commands, "status", "--porcelain=v2", "-z", "--untracked-files=all", "--ignore-submodules=dirty")
	if err != nil {
		return workingState{}, err
	}
	status, err := parsePorcelainV2(statusResult.Stdout)
	if err != nil {
		return workingState{}, err
	}
	status = applyStatusRestrictedPatterns(status, restrictedPatterns)
	stagedScope := diffScope{cached: true}
	if headSHA != "" {
		stagedScope.revisions = []string{headSHA}
	}
	stagedRaw, err := c.diff(ctx, repository, commands, stagedScope, []string{"--raw", "--no-abbrev", "-z"}, nil)
	if err != nil {
		return workingState{}, err
	}
	unstagedRaw, err := c.diff(ctx, repository, commands, diffScope{}, []string{"--raw", "--no-abbrev", "-z"}, nil)
	if err != nil {
		return workingState{}, err
	}
	statePathspecs := safeStatePathspecs(status, restrictedPatterns)
	stagedPatch, err := c.diff(ctx, repository, commands, stagedScope, []string{"--patch", "--full-index"}, statePathspecs)
	if err != nil {
		return workingState{}, err
	}
	unstagedPatch, err := c.diff(ctx, repository, commands, diffScope{}, []string{"--patch", "--full-index"}, statePathspecs)
	if err != nil {
		return workingState{}, err
	}
	statMetadata, err := statusStatMetadata(repository, status)
	if err != nil {
		return workingState{}, err
	}
	return workingState{
		status:       status,
		statMetadata: statMetadata,
		fingerprint: fingerprint(
			[]byte(headSHA),
			statusResult.Stdout,
			stagedRaw,
			unstagedRaw,
			stagedPatch,
			unstagedPatch,
			statMetadata,
		),
	}, nil
}

func statusStatMetadata(repository string, status WorktreeStatus) ([]byte, error) {
	paths := make([]string, 0, len(status.Entries))
	seen := make(map[string]struct{}, len(status.Entries))
	for _, entry := range status.Entries {
		if entry.Path == "" {
			continue
		}
		if _, exists := seen[entry.Path]; exists {
			continue
		}
		seen[entry.Path] = struct{}{}
		paths = append(paths, entry.Path)
	}
	sort.Strings(paths)
	var builder strings.Builder
	for _, relative := range paths {
		absolute, err := repositoryPath(repository, relative)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(absolute)
		if errors.Is(err, os.ErrNotExist) {
			builder.WriteString(strconv.Quote(relative))
			builder.WriteString(" missing\n")
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat changed path %q: %w", relative, err)
		}
		builder.WriteString(strconv.Quote(relative))
		builder.WriteByte(' ')
		builder.WriteString(info.Mode().String())
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatInt(info.Size(), 10))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
		builder.WriteByte('\n')
	}
	return []byte(builder.String()), nil
}

func repositoryPath(repository, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("unsafe repository-relative path %q", relative)
	}
	cleanRelative := filepath.Clean(filepath.FromSlash(relative))
	if cleanRelative == ".." || strings.HasPrefix(cleanRelative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("repository-relative path escapes root: %q", relative)
	}
	absolute := filepath.Join(repository, cleanRelative)
	within, err := filepath.Rel(repository, absolute)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("repository-relative path escapes root: %q", relative)
	}
	return absolute, nil
}

func (c *Collector) resolveCommit(
	ctx context.Context,
	repository string,
	commands *[]CommandMetadata,
	reference string,
) (string, error) {
	result, err := c.execute(ctx, repository, commands, "rev-parse", "--verify", "--end-of-options", reference+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(result.Stdout))
	if len(sha) < 40 {
		return "", fmt.Errorf("Git returned invalid commit SHA %q for %q", sha, reference)
	}
	return sha, nil
}

func (c *Collector) mergeBase(
	ctx context.Context,
	repository string,
	commands *[]CommandMetadata,
	leftSHA, rightSHA string,
) (string, error) {
	result, err := c.execute(ctx, repository, commands, "merge-base", leftSHA, rightSHA)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(result.Stdout))
	if len(sha) < 40 {
		return "", fmt.Errorf("Git returned invalid merge-base SHA %q", sha)
	}
	return sha, nil
}

func (c *Collector) execute(
	ctx context.Context,
	repository string,
	commands *[]CommandMetadata,
	args ...string,
) (CommandResult, error) {
	result, err := c.runner.Run(ctx, repository, args...)
	metadata := CommandMetadata{
		Args:       sanitizedCommandArgs(args),
		ExitCode:   result.ExitCode,
		Duration:   result.Duration,
		Successful: err == nil,
	}
	*commands = append(*commands, metadata)
	return result, err
}

func sanitizedCommandArgs(args []string) []string {
	result := make([]string, 0, len(args))
	pathspecCount := 0
	for _, argument := range args {
		if strings.HasPrefix(argument, ":(top,exclude,") {
			pathspecCount++
			continue
		}
		result = append(result, argument)
	}
	if pathspecCount > 0 {
		result = append(result, fmt.Sprintf("<%d excluded pathspecs>", pathspecCount))
	}
	return result
}

func isExitCode(err error, code int) bool {
	var commandErr *CommandError
	return errors.As(err, &commandErr) && commandErr.Result.ExitCode == code
}

func isCommandFailure(err error) bool {
	var commandErr *CommandError
	return errors.As(err, &commandErr)
}

func parseCommitRange(value string) (from, to string, threeDot bool, err error) {
	value = strings.TrimSpace(value)
	separator := ".."
	if strings.Contains(value, "...") {
		separator = "..."
		threeDot = true
	}
	parts := strings.Split(value, separator)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false, fmt.Errorf("commit range must be <from>..<to> or <from>...<to>, got %q", value)
	}
	from = strings.TrimSpace(parts[0])
	to = strings.TrimSpace(parts[1])
	if strings.HasPrefix(from, "-") || strings.HasPrefix(to, "-") {
		return "", "", false, fmt.Errorf("commit range refs cannot begin with '-'")
	}
	return from, to, threeDot, nil
}

func fingerprint(parts ...[]byte) string {
	hash := sha256.New()
	var length [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write(part)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func joinPatches(left, right string) string {
	left = strings.TrimRight(left, "\n")
	right = strings.TrimLeft(right, "\n")
	if left == "" {
		if right == "" {
			return ""
		}
		return right + "\n"
	}
	if right == "" {
		return left + "\n"
	}
	return left + "\n" + right
}
