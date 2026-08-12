package contextpack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/signaturekey/zephyr/internal/redaction"
	"github.com/signaturekey/zephyr/internal/safefile"
)

const Version = 1

type Repository struct {
	Root        string `json:"root"`
	Branch      string `json:"branch,omitempty"`
	Head        string `json:"head,omitempty"`
	BaseRef     string `json:"base_ref,omitempty"`
	BaseSHA     string `json:"base_sha,omitempty"`
	TargetSHA   string `json:"target_sha,omitempty"`
	MergeBase   string `json:"merge_base,omitempty"`
	CommitRange string `json:"commit_range,omitempty"`
}

type Diff struct {
	Full       string `json:"full,omitempty"`
	Staged     string `json:"staged,omitempty"`
	Unstaged   string `json:"unstaged,omitempty"`
	Truncated  bool   `json:"truncated"`
	TotalBytes int64  `json:"total_bytes"`
}

type Document struct {
	Kind        string    `json:"kind"`
	Path        string    `json:"path"`
	ContentHash string    `json:"content_hash"`
	FetchedAt   time.Time `json:"fetched_at,omitempty"`
	Content     string    `json:"content"`
	Truncated   bool      `json:"truncated"`
}

type BusinessSnapshot struct {
	Source      string    `json:"source"`
	Key         string    `json:"key,omitempty"`
	URL         string    `json:"url,omitempty"`
	FetchedAt   time.Time `json:"fetched_at"`
	ContentHash string    `json:"content_hash"`
	Content     string    `json:"content"`
}

type BusinessSnapshotInput struct {
	Source    string
	Key       string
	URL       string
	FetchedAt time.Time
	Content   string
}

type CoverageLimit struct {
	Source string `json:"source"`
	Reason string `json:"reason"`
}

type SourceManifest struct {
	Included    []string        `json:"included"`
	Excluded    []CoverageLimit `json:"excluded"`
	Unavailable []CoverageLimit `json:"unavailable"`
}

type Packet struct {
	Version              int                `json:"version"`
	RunID                string             `json:"run_id"`
	Mode                 string             `json:"mode"`
	Source               string             `json:"source"`
	Repository           Repository         `json:"repository"`
	GitMetadata          json.RawMessage    `json:"git_metadata,omitempty"`
	ChangedFiles         []string           `json:"changed_files"`
	Technologies         []string           `json:"technologies"`
	Diff                 Diff               `json:"diff"`
	Plan                 *Document          `json:"plan,omitempty"`
	BusinessContext      []BusinessSnapshot `json:"business_context"`
	ProjectInstructions  []Document         `json:"project_instructions"`
	Sources              SourceManifest     `json:"sources"`
	RoutingSignals       []string           `json:"routing_signals"`
	StrongRoutingSignals []string           `json:"strong_routing_signals"`
	CoverageLimits       []string           `json:"coverage_limits"`
	Restrictions         []string           `json:"restrictions"`
}

type Truncation struct {
	Path          string `json:"path"`
	OriginalBytes int64  `json:"original_bytes"`
	IncludedBytes int64  `json:"included_bytes"`
	Reason        string `json:"reason"`
}

type Result struct {
	Packet      Packet       `json:"packet"`
	Truncations []Truncation `json:"truncations"`
}

type InstructionSnapshot struct {
	Version     int             `json:"version"`
	Documents   []Document      `json:"documents"`
	Truncations []Truncation    `json:"truncations"`
	Excluded    []CoverageLimit `json:"excluded"`
}

type Options struct {
	RunDir             string
	RunID              string
	Mode               string
	Source             string
	RepoRoot           string
	Repository         Repository
	ChangedFiles       []string
	PlanPath           string
	MaxDiffBytes       int64
	MaxDocumentBytes   int64
	MaxBusinessBytes   int64
	Redaction          redaction.Policy
	Instructions       InstructionSnapshot
	ExcludedSources    []CoverageLimit
	UnavailableSources []CoverageLimit
}

func Build(opts Options) (Result, error) {
	if opts.RunDir == "" || opts.RunID == "" || opts.Mode == "" {
		return Result{}, errors.New("run directory, run ID, and mode are required")
	}
	if opts.MaxDiffBytes <= 0 {
		opts.MaxDiffBytes = 2 << 20
	}
	if opts.MaxDocumentBytes <= 0 {
		opts.MaxDocumentBytes = 512 << 10
	}
	if opts.MaxBusinessBytes <= 0 {
		opts.MaxBusinessBytes = 4 << 20
	}
	if opts.Redaction.DenyPatterns == nil {
		opts.Redaction = redaction.DefaultPolicy(nil)
	}

	reviewRepository := sanitizeRepository(opts.Repository, opts.Redaction)
	reviewRepository.Root = "reviewed-repository"
	packet := Packet{
		Version:              Version,
		RunID:                opts.RunID,
		Mode:                 opts.Mode,
		Source:               opts.Source,
		Repository:           reviewRepository,
		ChangedFiles:         uniqueSorted(opts.Redaction.Strings(opts.ChangedFiles)),
		Technologies:         []string{},
		BusinessContext:      []BusinessSnapshot{},
		ProjectInstructions:  []Document{},
		RoutingSignals:       []string{},
		StrongRoutingSignals: []string{},
		CoverageLimits:       []string{},
		Sources: SourceManifest{
			Included:    []string{},
			Excluded:    append([]CoverageLimit{}, opts.ExcludedSources...),
			Unavailable: append([]CoverageLimit{}, opts.UnavailableSources...),
		},
		Restrictions: []string{
			"read-only review",
			"untracked file content excluded unless explicitly included by the harness",
			"generated, vendor, binary, and secret paths may be excluded by policy",
		},
	}
	packet.Technologies = detectTechnologies(packet.ChangedFiles)

	var truncations []Truncation
	metadataPath := filepath.Join(opts.RunDir, "git", "metadata.json")
	if data, err := os.ReadFile(metadataPath); err == nil {
		data = []byte(opts.Redaction.Text(string(data)))
		data = sanitizeGitMetadata(data)
		if json.Valid(data) {
			packet.GitMetadata = append(json.RawMessage(nil), data...)
			packet.Sources.Included = append(packet.Sources.Included, metadataPath)
		} else {
			packet.Sources.Excluded = append(packet.Sources.Excluded, CoverageLimit{Source: metadataPath, Reason: "invalid JSON"})
		}
	} else if !errors.Is(err, fs.ErrNotExist) && opts.Source != "plan-only" {
		return Result{}, fmt.Errorf("read Git metadata: %w", err)
	}

	diff, diffTruncations, included, err := collectDiff(opts)
	if err != nil {
		return Result{}, err
	}
	packet.Diff = diff
	truncations = append(truncations, diffTruncations...)
	packet.Sources.Included = append(packet.Sources.Included, included...)

	if opts.PlanPath != "" {
		doc, trunc, err := readDocument("plan", opts.PlanPath, opts.MaxDocumentBytes, opts.Redaction)
		if err != nil {
			packet.Sources.Unavailable = append(packet.Sources.Unavailable, CoverageLimit{Source: opts.PlanPath, Reason: err.Error()})
		} else {
			packet.Plan = &doc
			packet.Plan.Path = filepath.Base(doc.Path)
			packet.Sources.Included = append(packet.Sources.Included, opts.PlanPath)
			if trunc != nil {
				truncations = append(truncations, *trunc)
			}
		}
	}

	instructions, instructionTruncations, excluded, err := collectInstructions(opts)
	if err != nil {
		return Result{}, err
	}
	packet.ProjectInstructions = instructions
	if packet.ProjectInstructions == nil {
		packet.ProjectInstructions = []Document{}
	}
	truncations = append(truncations, instructionTruncations...)
	packet.Sources.Excluded = append(packet.Sources.Excluded, excluded...)
	for _, doc := range instructions {
		packet.Sources.Included = append(packet.Sources.Included, doc.Path)
	}

	business, businessIncluded, businessExcluded, businessTruncations, err := collectBusiness(opts)
	if err != nil {
		return Result{}, err
	}
	packet.BusinessContext = business
	if packet.BusinessContext == nil {
		packet.BusinessContext = []BusinessSnapshot{}
	}
	packet.Sources.Included = append(packet.Sources.Included, businessIncluded...)
	packet.Sources.Excluded = append(packet.Sources.Excluded, businessExcluded...)
	truncations = append(truncations, businessTruncations...)

	packet.Sources.Included = uniqueSorted(packet.Sources.Included)
	for index := range packet.Sources.Included {
		packet.Sources.Included[index] = logicalSource(opts.RunDir, packet.Sources.Included[index])
	}
	packet.Sources.Included = uniqueSorted(opts.Redaction.Strings(packet.Sources.Included))
	for index := range packet.Sources.Excluded {
		packet.Sources.Excluded[index].Source = logicalSource(opts.RunDir, packet.Sources.Excluded[index].Source)
		packet.Sources.Excluded[index].Source = opts.Redaction.Text(packet.Sources.Excluded[index].Source)
		packet.Sources.Excluded[index].Reason = opts.Redaction.Text(packet.Sources.Excluded[index].Reason)
	}
	for index := range packet.Sources.Unavailable {
		packet.Sources.Unavailable[index].Source = logicalSource(opts.RunDir, packet.Sources.Unavailable[index].Source)
		packet.Sources.Unavailable[index].Source = opts.Redaction.Text(packet.Sources.Unavailable[index].Source)
		packet.Sources.Unavailable[index].Reason = opts.Redaction.Text(packet.Sources.Unavailable[index].Reason)
	}
	sortCoverage(packet.Sources.Excluded)
	sortCoverage(packet.Sources.Unavailable)
	for index := range truncations {
		truncations[index].Path = logicalSource(opts.RunDir, truncations[index].Path)
		truncations[index].Path = opts.Redaction.Text(truncations[index].Path)
		truncations[index].Reason = opts.Redaction.Text(truncations[index].Reason)
	}
	sort.Slice(truncations, func(i, j int) bool { return truncations[i].Path < truncations[j].Path })
	packet.RoutingSignals = detectRoutingSignals(packet)
	packet.StrongRoutingSignals = detectStrongRoutingSignals(packet)
	packet.CoverageLimits = coverageLimitStrings(packet.Sources, truncations)

	return Result{Packet: packet, Truncations: truncations}, nil
}

func SaveBusinessSnapshot(runDir string, input BusinessSnapshotInput, policy redaction.Policy) (string, BusinessSnapshot, error) {
	input.Source = strings.ToLower(strings.TrimSpace(input.Source))
	if input.Source != "jira" && input.Source != "confluence" && input.Source != "bitbucket" {
		return "", BusinessSnapshot{}, fmt.Errorf("unsupported business source %q", input.Source)
	}
	input.Key = strings.TrimSpace(input.Key)
	if input.Key == "" {
		return "", BusinessSnapshot{}, errors.New("business snapshot key is required")
	}
	if strings.TrimSpace(input.Content) == "" {
		return "", BusinessSnapshot{}, errors.New("business snapshot content is required")
	}
	if input.FetchedAt.IsZero() {
		input.FetchedAt = time.Now().UTC()
	}
	if policy.DenyPatterns == nil {
		policy = redaction.DefaultPolicy(nil)
	}

	content := policy.Text(input.Content)
	snapshot := BusinessSnapshot{
		Source:      input.Source,
		Key:         policy.Text(input.Key),
		URL:         policy.Text(strings.TrimSpace(input.URL)),
		FetchedAt:   input.FetchedAt.UTC(),
		ContentHash: "sha256:" + hash(content),
		Content:     content,
	}
	name := safeSnapshotName(snapshot.Key) + ".json"
	path := filepath.Join(runDir, "context", input.Source, name)
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", BusinessSnapshot{}, fmt.Errorf("encode business snapshot: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWrite(path, data, 0o600); err != nil {
		return "", BusinessSnapshot{}, err
	}
	return path, snapshot, nil
}

func sanitizeRepository(value Repository, policy redaction.Policy) Repository {
	value.Root = policy.Text(value.Root)
	value.Branch = policy.Text(value.Branch)
	value.Head = policy.Text(value.Head)
	value.BaseRef = policy.Text(value.BaseRef)
	value.BaseSHA = policy.Text(value.BaseSHA)
	value.TargetSHA = policy.Text(value.TargetSHA)
	value.MergeBase = policy.Text(value.MergeBase)
	value.CommitRange = policy.Text(value.CommitRange)
	return value
}

func sanitizeGitMetadata(data []byte) []byte {
	var metadata map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		return data
	}
	if repository, ok := metadata["repository"].(map[string]any); ok {
		repository["root"] = "reviewed-repository"
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return data
	}
	return encoded
}

func logicalSource(runDir, value string) string {
	if !filepath.IsAbs(value) {
		return filepath.ToSlash(value)
	}
	relative, err := filepath.Rel(runDir, value)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(relative)
	}
	return filepath.Base(value)
}

func collectDiff(opts Options) (Diff, []Truncation, []string, error) {
	type target struct {
		name string
		path string
		size int64
	}
	targets := []target{
		{name: "full", path: filepath.Join(opts.RunDir, "git", "diff.patch")},
		{name: "staged", path: filepath.Join(opts.RunDir, "git", "staged.patch")},
		{name: "unstaged", path: filepath.Join(opts.RunDir, "git", "unstaged.patch")},
	}

	available := make([]target, 0, len(targets))
	for _, item := range targets {
		info, err := os.Stat(item.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Diff{}, nil, nil, fmt.Errorf("inspect %s diff: %w", item.name, err)
		}
		if !info.Mode().IsRegular() {
			return Diff{}, nil, nil, fmt.Errorf("%s diff artifact is not a regular file", item.name)
		}
		item.size = info.Size()
		available = append(available, item)
	}

	for _, item := range available {
		if item.name == "full" && item.size > 0 {
			available = []target{item}
			break
		}
	}

	result := Diff{}
	var truncations []Truncation
	var included []string
	remaining := opts.MaxDiffBytes
	for _, item := range available {
		result.TotalBytes += item.size
		included = append(included, item.path)
		limit := remaining
		if limit < 0 {
			limit = 0
		}
		data, err := readFilePrefix(item.path, limit)
		if err != nil {
			return Diff{}, nil, nil, fmt.Errorf("read %s diff: %w", item.name, err)
		}
		content := string(data)
		var trunc *Truncation
		if item.size > limit {
			content, trunc = truncateCaptured(item.path, data, item.size, limit, "total diff size limit")
		}
		content = opts.Redaction.Text(content)
		if trunc != nil {
			result.Truncated = true
			truncations = append(truncations, *trunc)
		}
		remaining -= item.size
		switch item.name {
		case "full":
			result.Full = content
		case "staged":
			result.Staged = content
		case "unstaged":
			result.Unstaged = content
		}
	}
	return result, truncations, included, nil
}

func readFilePrefix(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, maximum))
}

func collectInstructions(opts Options) ([]Document, []Truncation, []CoverageLimit, error) {
	if opts.Instructions.Version != 0 && opts.Instructions.Version != Version {
		return nil, nil, nil, fmt.Errorf("instruction snapshot version %d is unsupported", opts.Instructions.Version)
	}
	docs := append([]Document{}, opts.Instructions.Documents...)
	truncations := append([]Truncation{}, opts.Instructions.Truncations...)
	excluded := append([]CoverageLimit{}, opts.Instructions.Excluded...)
	return docs, truncations, excluded, nil
}

func SnapshotInstructions(repoRoot string, allowed []string, maximum int64, policy redaction.Policy) InstructionSnapshot {
	snapshot := InstructionSnapshot{
		Version: Version, Documents: []Document{}, Truncations: []Truncation{}, Excluded: []CoverageLimit{},
	}
	for _, rel := range uniqueSorted(allowed) {
		if policy.PathDenied(rel) {
			snapshot.Excluded = append(snapshot.Excluded, CoverageLimit{Source: "project-instruction", Reason: "path denied by redaction policy"})
			continue
		}
		doc, trunc, err := readDocumentBeneath("project-instruction", repoRoot, rel, maximum, policy)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			snapshot.Excluded = append(snapshot.Excluded, CoverageLimit{Source: filepath.ToSlash(rel), Reason: err.Error()})
			continue
		}
		doc.Path = filepath.ToSlash(rel)
		snapshot.Documents = append(snapshot.Documents, doc)
		if trunc != nil {
			trunc.Path = doc.Path
			snapshot.Truncations = append(snapshot.Truncations, *trunc)
		}
	}
	sort.Slice(snapshot.Documents, func(i, j int) bool { return snapshot.Documents[i].Path < snapshot.Documents[j].Path })
	sort.Slice(snapshot.Truncations, func(i, j int) bool { return snapshot.Truncations[i].Path < snapshot.Truncations[j].Path })
	sortCoverage(snapshot.Excluded)
	return snapshot
}

func InstructionCandidates(changedFiles []string) []string {
	set := map[string]struct{}{"AGENTS.md": {}}
	for _, changed := range changedFiles {
		clean := path.Clean(filepath.ToSlash(changed))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			continue
		}
		dir := path.Dir(clean)
		for dir != "." && dir != "/" {
			set[path.Join(dir, "AGENTS.md")] = struct{}{}
			dir = path.Dir(dir)
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func collectBusiness(opts Options) ([]BusinessSnapshot, []string, []CoverageLimit, []Truncation, error) {
	root := filepath.Join(opts.RunDir, "context")
	var paths []string
	for _, source := range []string{"jira", "confluence", "bitbucket"} {
		dir := filepath.Join(root, source)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("read %s snapshots: %w", source, err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				paths = append(paths, filepath.Join(dir, entry.Name()))
			}
		}
	}
	sort.Strings(paths)

	var snapshots []BusinessSnapshot
	var included []string
	var excluded []CoverageLimit
	var truncations []Truncation
	remaining := opts.MaxBusinessBytes
	for _, path := range paths {
		relative, relErr := filepath.Rel(opts.RunDir, path)
		if relErr != nil {
			excluded = append(excluded, CoverageLimit{Source: path, Reason: "snapshot path is outside run"})
			continue
		}
		data, err := safefile.ReadBeneath(opts.RunDir, filepath.ToSlash(relative), 20<<20)
		if err != nil {
			excluded = append(excluded, CoverageLimit{Source: path, Reason: err.Error()})
			continue
		}
		var snapshot BusinessSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil {
			excluded = append(excluded, CoverageLimit{Source: path, Reason: "invalid snapshot JSON"})
			continue
		}
		if snapshot.Source == "" || snapshot.FetchedAt.IsZero() || snapshot.Content == "" {
			excluded = append(excluded, CoverageLimit{Source: path, Reason: "missing source, fetched_at, or content"})
			continue
		}
		computed := hash(snapshot.Content)
		if snapshot.ContentHash == "" {
			snapshot.ContentHash = "sha256:" + computed
		} else if snapshot.ContentHash != computed && snapshot.ContentHash != "sha256:"+computed {
			excluded = append(excluded, CoverageLimit{Source: path, Reason: "content hash mismatch"})
			continue
		}
		snapshot.Key = opts.Redaction.Text(snapshot.Key)
		snapshot.URL = opts.Redaction.Text(snapshot.URL)
		snapshot.Content = opts.Redaction.Text(snapshot.Content)
		limit := remaining
		if limit < 0 {
			limit = 0
		}
		originalSize := int64(len(snapshot.Content))
		content, trunc := truncateText(path, []byte(snapshot.Content), limit, "total business context size limit")
		snapshot.Content = content
		if trunc != nil {
			truncations = append(truncations, *trunc)
		}
		remaining -= originalSize
		snapshots = append(snapshots, snapshot)
		included = append(included, path)
	}
	return snapshots, included, excluded, truncations, nil
}

func readDocument(kind, path string, limit int64, policy redaction.Policy) (Document, *Truncation, error) {
	file, err := os.Open(path)
	if err != nil {
		return Document{}, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Document{}, nil, err
	}
	if !info.Mode().IsRegular() {
		return Document{}, nil, fmt.Errorf("%s is not a regular file", path)
	}
	return readDocumentStream(kind, path, file, info.Size(), limit, policy)
}

func readDocumentBeneath(kind, root, relative string, limit int64, policy redaction.Policy) (Document, *Truncation, error) {
	file, err := safefile.OpenBeneath(root, relative)
	if err != nil {
		return Document{}, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Document{}, nil, err
	}
	return readDocumentStream(kind, filepath.ToSlash(relative), file, info.Size(), limit, policy)
}

func readDocumentStream(kind, displayPath string, reader io.Reader, declaredSize, limit int64, policy redaction.Policy) (Document, *Truncation, error) {
	if limit < 0 {
		limit = 0
	}
	readLimit := limit + 1
	if readLimit < limit {
		readLimit = limit
	}
	data, err := io.ReadAll(io.LimitReader(reader, readLimit))
	if err != nil {
		return Document{}, nil, err
	}
	total := int64(len(data))
	if declaredSize > total {
		total = declaredSize
	}
	captured := data
	if int64(len(captured)) > limit {
		captured = captured[:limit]
	}
	content := string(captured)
	var trunc *Truncation
	if total > limit {
		content, trunc = truncateCaptured(displayPath, captured, total, limit, "document size limit")
	}
	content = policy.Text(content)
	digest := sha256.Sum256(captured)
	return Document{
		Kind:        kind,
		Path:        policy.Text(displayPath),
		ContentHash: "sha256:" + hex.EncodeToString(digest[:]),
		Content:     content,
		Truncated:   trunc != nil,
	}, trunc, nil
}

func truncateCaptured(path string, prefix []byte, total, limit int64, reason string) (string, *Truncation) {
	if limit < 0 {
		limit = 0
	}
	marker := []byte("\n[ZEPHYR TRUNCATED]\n")
	contentLimit := limit - int64(len(marker))
	if contentLimit < 0 {
		contentLimit = 0
	}
	if int64(len(prefix)) > contentLimit {
		prefix = prefix[:contentLimit]
	}
	content := append(append([]byte{}, prefix...), marker...)
	return string(content), &Truncation{Path: path, OriginalBytes: total, IncludedBytes: limit, Reason: reason}
}

func truncateText(path string, data []byte, limit int64, reason string) (string, *Truncation) {
	if int64(len(data)) <= limit {
		return string(data), nil
	}
	if limit < 0 {
		limit = 0
	}
	marker := "\n[ZEPHYR TRUNCATED]\n"
	markerBytes := int64(len(marker))
	contentLimit := limit - markerBytes
	if contentLimit < 0 {
		contentLimit = 0
	}
	return string(data[:contentLimit]) + marker, &Truncation{
		Path:          path,
		OriginalBytes: int64(len(data)),
		IncludedBytes: limit,
		Reason:        reason,
	}
}

func detectTechnologies(paths []string) []string {
	set := map[string]struct{}{}
	for _, path := range paths {
		lower := strings.ToLower(filepath.ToSlash(path))
		switch {
		case strings.HasSuffix(lower, ".pb.go"):
			set["go"] = struct{}{}
			set["protobuf"] = struct{}{}
		case strings.HasSuffix(lower, ".sql") || strings.Contains(lower, "/migrations/") || strings.HasPrefix(lower, "migrations/"):
			set["sql"] = struct{}{}
		case strings.HasSuffix(lower, ".proto"):
			set["protobuf"] = struct{}{}
		case strings.Contains(lower, "openapi") || strings.Contains(lower, "/brief/") || strings.HasPrefix(lower, "brief/"):
			set["api-contract"] = struct{}{}
		case strings.HasSuffix(lower, ".go"):
			set["go"] = struct{}{}
		case strings.HasSuffix(lower, ".tsx"):
			set["frontend"] = struct{}{}
			set["typescript"] = struct{}{}
		case strings.HasSuffix(lower, ".ts"):
			set["typescript"] = struct{}{}
		case filepath.Base(lower) == "tsconfig.json" || strings.HasPrefix(filepath.Base(lower), "tsconfig."):
			set["typescript"] = struct{}{}
		case strings.HasSuffix(lower, ".jsx"):
			set["frontend"] = struct{}{}
			set["javascript"] = struct{}{}
		case strings.HasSuffix(lower, ".js"):
			set["javascript"] = struct{}{}
		case strings.HasSuffix(lower, ".css") || strings.HasSuffix(lower, ".scss") || strings.HasSuffix(lower, ".less"):
			set["css"] = struct{}{}
			set["frontend"] = struct{}{}
		case filepath.Base(lower) == "skill.md":
			set["codex-skill"] = struct{}{}
			set["markdown"] = struct{}{}
		case strings.HasSuffix(lower, ".md"):
			set["markdown"] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func detectRoutingSignals(packet Packet) []string {
	var signals []string
	if packet.Mode == "plan" || packet.Mode == "alignment" {
		signals = append(signals, "architecture")
	}
	combined := routingCorpus(packet, 2<<20)
	for _, group := range []struct {
		signal string
		terms  []string
	}{
		{signal: "security", terms: []string{"auth", "permission", "role", "token", "credential", "secret", "pii", "personal data", "idor", "xss", "dangerouslysetinnerhtml", "localstorage"}},
		{signal: "sql", terms: []string{" sql ", "migration", "alter table", "create table", "drop table", "transaction", "database"}},
		{signal: "contract", terms: []string{"openapi", "protobuf", "proto contract", "api contract", "public dto", "backward compatibility", "event contract"}},
		{signal: "typescript", terms: []string{"typescript", "tsconfig", "type narrowing", "type assertion", "@ts-ignore"}},
		{signal: "frontend", terms: []string{"frontend", "react", "useeffect", "usestate", "react query", "redux", "browser", "accessibility", "a11y"}},
		{signal: "skill-authoring", terms: []string{"skill.md", "frontmatter", "progressive disclosure", "skill-validator", "trigger description"}},
		{signal: "reliability", terms: []string{"timeout", "retry", "circuit breaker", "backpressure", "idempotent", "graceful shutdown", "readiness", "service level objective", "slo"}},
		{signal: "messaging", terms: []string{"kafka", "message broker", "consumer offset", "dead letter", "dlq", "at-least-once", "message queue", "databus"}},
		{signal: "infrastructure", terms: []string{"dockerfile", "kubernetes", "helm", "deployment manifest", "readiness probe", "liveness probe", "ci/cd", "teamcity"}},
		{signal: "storage", terms: []string{"redis", "cache invalidation", "elasticsearch", "opensearch", "search index", "object storage", "time to live", "ttl"}},
		{signal: "tests", terms: []string{"acceptance criteria", "test scenario", "negative test", "boundary case"}},
		{signal: "observable-behavior", terms: []string{"observable behavior", "endpoint", "rpc method", "handler", "user-visible"}},
	} {
		for _, term := range group.terms {
			if strings.Contains(combined, term) {
				signals = append(signals, group.signal)
				break
			}
		}
	}

	serviceRoots := map[string]struct{}{}
	for _, path := range packet.ChangedFiles {
		normalized := filepath.ToSlash(path)
		parts := strings.Split(normalized, "/")
		if len(parts) > 2 && (parts[0] == "services" || parts[0] == "service" || parts[0] == "cmd") {
			serviceRoots[parts[0]+"/"+parts[1]] = struct{}{}
		} else if len(parts) > 1 && strings.HasPrefix(parts[0], "service-") {
			serviceRoots[parts[0]] = struct{}{}
		}
		lower := strings.ToLower(normalized)
		base := filepath.Base(lower)
		isTypeScript := strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".tsx") ||
			base == "tsconfig.json" || strings.HasPrefix(base, "tsconfig.")
		isFrontend := strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".jsx") ||
			strings.HasSuffix(lower, ".css") || strings.HasSuffix(lower, ".scss") || strings.HasSuffix(lower, ".less")
		isSkill := base == "skill.md" || base == "agents.md" || base == "claude.md" || strings.HasPrefix(lower, "template/") || strings.Contains(lower, "/skills/")
		pathWithBoundaries := "/" + strings.Trim(lower, "/") + "/"
		isReliability := strings.Contains(pathWithBoundaries, "/resilience/") || strings.Contains(base, "retry") || strings.Contains(base, "circuit")
		isMessaging := strings.Contains(pathWithBoundaries, "/kafka/") || strings.Contains(pathWithBoundaries, "/messaging/") ||
			strings.Contains(pathWithBoundaries, "/queues/") || strings.Contains(pathWithBoundaries, "/databus/")
		isInfrastructure := base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") || base == ".gitlab-ci.yml" ||
			strings.Contains(pathWithBoundaries, "/k8s/") || strings.Contains(pathWithBoundaries, "/kubernetes/") ||
			strings.Contains(pathWithBoundaries, "/helm/") || strings.Contains(pathWithBoundaries, "/charts/") ||
			strings.Contains(pathWithBoundaries, "/.github/workflows/") || strings.Contains(pathWithBoundaries, "/teamcity/")
		isStorage := strings.Contains(pathWithBoundaries, "/redis/") || strings.Contains(pathWithBoundaries, "/cache/") ||
			strings.Contains(pathWithBoundaries, "/elasticsearch/") || strings.Contains(pathWithBoundaries, "/opensearch/") ||
			strings.Contains(pathWithBoundaries, "/search/")
		if isTypeScript {
			signals = append(signals, "typescript")
		}
		if isFrontend {
			signals = append(signals, "frontend")
		}
		if isSkill {
			signals = append(signals, "skill-authoring")
		}
		if isReliability {
			signals = append(signals, "reliability")
		}
		if isMessaging {
			signals = append(signals, "messaging")
		}
		if isInfrastructure {
			signals = append(signals, "infrastructure")
		}
		if isStorage {
			signals = append(signals, "storage")
		}
		if strings.HasSuffix(lower, "_test.go") || strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
			strings.Contains(lower, "__tests__/") || strings.Contains(lower, ".spec.") || strings.Contains(lower, ".test.") ||
			strings.Contains(lower, ".cspec.") {
			signals = append(signals, "tests")
		} else if strings.HasSuffix(lower, ".go") || isTypeScript || isFrontend || strings.HasSuffix(lower, ".sql") || strings.HasSuffix(lower, ".proto") ||
			strings.Contains(lower, "openapi") || strings.Contains(lower, "/brief/") {
			signals = append(signals, "observable-behavior")
		}
		if filepath.Base(lower) == "go.mod" || filepath.Base(lower) == "go.work" {
			signals = append(signals, "new-module")
		}
	}
	if len(serviceRoots) > 1 {
		signals = append(signals, "multiple-services")
	}
	if changedLineCount(packet.Diff.Full) >= 500 {
		signals = append(signals, "large-complexity-delta")
	}
	return uniqueSorted(signals)
}

// detectStrongRoutingSignals limits deterministic protection to signals derived
// from the changed paths or diff. Plan and business-context prose remains input
// to semantic routing and cannot protect a role merely because it contains a
// matching word.
func detectStrongRoutingSignals(packet Packet) []string {
	strong := packet
	strong.Mode = ""
	strong.Plan = nil
	strong.BusinessContext = nil
	return detectRoutingSignals(strong)
}

func routingCorpus(packet Packet, maximum int) string {
	var builder strings.Builder
	appendBounded := func(value string) {
		if value == "" || builder.Len() >= maximum {
			return
		}
		remaining := maximum - builder.Len()
		if len(value) > remaining {
			value = value[:remaining]
		}
		builder.WriteString(value)
		builder.WriteByte('\n')
	}
	appendBounded(strings.Join(packet.ChangedFiles, "\n"))
	appendBounded(packet.Diff.Full)
	if packet.Plan != nil {
		appendBounded(packet.Plan.Content)
	}
	for _, business := range packet.BusinessContext {
		appendBounded(business.Content)
	}
	return strings.ToLower(builder.String())
}

func changedLineCount(diff string) int {
	count := 0
	for _, line := range strings.Split(diff, "\n") {
		if (strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++")) ||
			(strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---")) {
			count++
		}
	}
	return count
}

func coverageLimitStrings(sources SourceManifest, truncations []Truncation) []string {
	values := make([]string, 0, len(sources.Excluded)+len(sources.Unavailable)+len(truncations))
	for _, source := range sources.Excluded {
		values = append(values, source.Source+": "+source.Reason)
	}
	for _, source := range sources.Unavailable {
		values = append(values, source.Source+": "+source.Reason)
	}
	for _, truncation := range truncations {
		values = append(values, truncation.Path+": "+truncation.Reason)
	}
	return uniqueSorted(values)
}

func hash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
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

func sortCoverage(values []CoverageLimit) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Source == values[j].Source {
			return values[i].Reason < values[j].Reason
		}
		return values[i].Source < values[j].Source
	})
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(left, right string) bool {
	l, errLeft := filepath.Abs(left)
	r, errRight := filepath.Abs(right)
	return errLeft == nil && errRight == nil && filepath.Clean(l) == filepath.Clean(r)
}

func safeSnapshotName(value string) string {
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9', char == '-', char == '_', char == '.':
			builder.WriteRune(char)
		default:
			builder.WriteByte('-')
		}
	}
	name := strings.Trim(builder.String(), ".-")
	if name == "" {
		return "snapshot"
	}
	return name
}

func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return fmt.Errorf("create temporary snapshot: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("set snapshot mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close snapshot: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace snapshot: %w", err)
	}
	return nil
}
