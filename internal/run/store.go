package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

var (
	ErrInvalidRunID    = errors.New("invalid run id")
	ErrRunInsideRepo   = errors.New("run directory is inside the reviewed repository")
	ErrArtifactEscapes = errors.New("artifact path escapes run directory")
)

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() (string, error)
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type randomIDGenerator struct {
	reader io.Reader
}

func (g randomIDGenerator) NewID() (string, error) {
	buf := make([]byte, 6)
	if _, err := io.ReadFull(g.reader, buf); err != nil {
		return "", fmt.Errorf("read random run id: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

type StoreOption func(*Store)

func WithClock(clock Clock) StoreOption {
	return func(store *Store) {
		if clock != nil {
			store.clock = clock
		}
	}
}

func WithIDGenerator(generator IDGenerator) StoreOption {
	return func(store *Store) {
		if generator != nil {
			store.ids = generator
		}
	}
}

type Store struct {
	root  string
	clock Clock
	ids   IDGenerator
}

func NewStore(root string, options ...StoreOption) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("run store root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve run store root %q: %w", root, err)
	}
	store := &Store{
		root:  filepath.Clean(absolute),
		clock: realClock{},
		ids:   randomIDGenerator{reader: rand.Reader},
	}
	for _, option := range options {
		option(store)
	}
	return store, nil
}

func DefaultRoot() (string, error) {
	if value := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf("XDG_CACHE_HOME must be absolute: %q", value)
		}
		return filepath.Join(value, "zephyr", "runs"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for cache: %w", err)
	}
	return filepath.Join(home, ".cache", "zephyr", "runs"), nil
}

func NewDefaultStore(options ...StoreOption) (*Store, error) {
	root, err := DefaultRoot()
	if err != nil {
		return nil, err
	}
	return NewStore(root, options...)
}

func (s *Store) Root() string { return s.root }

func (s *Store) Lock(ctx context.Context, runID string) (func() error, error) {
	path, err := s.ArtifactPath(runID, ".lock")
	if err != nil {
		return nil, err
	}
	lock := flock.New(path)
	locked, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("lock run %q: %w", runID, err)
	}
	if !locked {
		_ = lock.Close()
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("lock run %q: %w", runID, err)
		}
		return nil, fmt.Errorf("lock run %q: lock was not acquired", runID)
	}
	return lock.Close, nil
}

type CreateOptions struct {
	Mode        Mode
	Source      Source
	Repository  string
	BaseRef     string
	CommitRange string
	PlanPath    string
}

func (s *Store) Create(ctx context.Context, options CreateOptions) (*Manifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	if options.Source == "" {
		if options.Mode == ModePlan {
			options.Source = SourcePlanOnly
		} else {
			options.Source = SourceWorkingTree
		}
	}
	if options.Mode == "" {
		options.Mode = ModeAuto
	}
	if err := options.Mode.Validate(); err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	if err := options.Source.Validate(); err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}
	if options.Source == SourceBranch && strings.TrimSpace(options.BaseRef) == "" {
		return nil, errors.New("create run: branch source requires --base")
	}
	if options.Source == SourceCommitRange && strings.TrimSpace(options.CommitRange) == "" {
		return nil, errors.New("create run: commit-range source requires a range")
	}

	repository, err := absoluteDirectory(options.Repository)
	if err != nil {
		return nil, fmt.Errorf("create run: resolve repository: %w", err)
	}
	planPath := ""
	if strings.TrimSpace(options.PlanPath) != "" {
		planPath, err = filepath.Abs(options.PlanPath)
		if err != nil {
			return nil, fmt.Errorf("create run: resolve plan path %q: %w", options.PlanPath, err)
		}
		planPath = filepath.Clean(planPath)
	}

	now := s.clock.Now().UTC()
	randomPart, err := s.ids.NewID()
	if err != nil {
		return nil, fmt.Errorf("create run id: %w", err)
	}
	if !validIDPart(randomPart) {
		return nil, fmt.Errorf("create run id %q: %w", randomPart, ErrInvalidRunID)
	}
	runID := now.Format("20060102T150405.000000000Z") + "-" + randomPart
	runDir := filepath.Join(s.root, runID)
	if err := ensureOutsideRepository(runDir, repository); err != nil {
		return nil, fmt.Errorf("create run %q: %w", runID, err)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return nil, fmt.Errorf("create run store %q: %w", s.root, err)
	}
	if err := os.Mkdir(runDir, 0o700); err != nil {
		return nil, fmt.Errorf("create run directory %q: %w", runDir, err)
	}
	for _, directory := range []string{
		"git", "context", "context/jira", "context/confluence", "context/bitbucket",
		"context/project-instructions", "packet", "candidates", "evidence",
	} {
		if err := os.Mkdir(filepath.Join(runDir, directory), 0o700); err != nil {
			return nil, fmt.Errorf("create run artifact directory %q: %w", directory, err)
		}
	}

	manifest := &Manifest{
		Version:     ManifestVersion,
		ID:          runID,
		RunDir:      runDir,
		CreatedAt:   now,
		UpdatedAt:   now,
		Mode:        options.Mode,
		Source:      options.Source,
		Repository:  repository,
		BaseRef:     strings.TrimSpace(options.BaseRef),
		CommitRange: strings.TrimSpace(options.CommitRange),
		PlanPath:    planPath,
		State:       StateCreated,
		Stages:      defaultStages(now),
	}
	if err := s.save(ctx, manifest, false); err != nil {
		return nil, err
	}
	return manifest, nil
}

func (s *Store) Load(ctx context.Context, runID string) (*Manifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("load run %q: %w", runID, err)
	}
	path, err := s.ArtifactPath(runID, "manifest.json")
	if err != nil {
		return nil, fmt.Errorf("load run: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load manifest %q: %w", path, err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode manifest %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode manifest %q: unexpected trailing JSON value", path)
		}
		return nil, fmt.Errorf("decode manifest %q trailing data: %w", path, err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate manifest %q: %w", path, err)
	}
	expectedDir := filepath.Join(s.root, runID)
	if manifest.ID != runID || filepath.Clean(manifest.RunDir) != expectedDir {
		return nil, fmt.Errorf("manifest identity does not match run %q", runID)
	}
	return &manifest, nil
}

func (s *Store) Save(ctx context.Context, manifest *Manifest) error {
	return s.save(ctx, manifest, true)
}

func (s *Store) save(ctx context.Context, manifest *Manifest, updateTimestamp bool) error {
	if manifest == nil {
		return errors.New("save manifest: manifest is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("save manifest %q: %w", manifest.ID, err)
	}
	if !validRunID(manifest.ID) {
		return fmt.Errorf("save manifest %q: %w", manifest.ID, ErrInvalidRunID)
	}
	expectedDir := filepath.Join(s.root, manifest.ID)
	if filepath.Clean(manifest.RunDir) != expectedDir {
		return fmt.Errorf("save manifest %q: run_dir %q does not match store", manifest.ID, manifest.RunDir)
	}
	if updateTimestamp {
		manifest.UpdatedAt = s.clock.Now().UTC()
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("save manifest %q: %w", manifest.ID, err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest %q: %w", manifest.ID, err)
	}
	data = append(data, '\n')
	if err := atomicWriteFile(ctx, filepath.Join(expectedDir, "manifest.json"), data, 0o600); err != nil {
		return fmt.Errorf("save manifest %q: %w", manifest.ID, err)
	}
	return nil
}

func (s *Store) ArtifactPath(runID string, elements ...string) (string, error) {
	if !validRunID(runID) {
		return "", fmt.Errorf("run id %q: %w", runID, ErrInvalidRunID)
	}
	base := filepath.Join(s.root, runID)
	parts := append([]string{base}, elements...)
	for _, element := range elements {
		if element == "" || filepath.IsAbs(element) {
			return "", fmt.Errorf("artifact element %q: %w", element, ErrArtifactEscapes)
		}
	}
	path := filepath.Clean(filepath.Join(parts...))
	if !pathWithin(path, base) {
		return "", fmt.Errorf("artifact path %q: %w", path, ErrArtifactEscapes)
	}
	return path, nil
}

func (s *Store) WriteArtifact(ctx context.Context, runID string, data []byte, elements ...string) (string, error) {
	path, err := s.ArtifactPath(runID, elements...)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create artifact parent for %q: %w", path, err)
	}
	if err := atomicWriteFile(ctx, path, data, 0o600); err != nil {
		return "", fmt.Errorf("write artifact %q: %w", path, err)
	}
	return path, nil
}

func (s *Store) WriteJSON(ctx context.Context, runID string, value any, elements ...string) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode JSON artifact %q: %w", strings.Join(elements, "/"), err)
	}
	return s.WriteArtifact(ctx, runID, append(data, '\n'), elements...)
}

func absoluteDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("repository is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", absolute)
	}
	return absolute, nil
}

func validIDPart(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return value != "." && value != ".."
}

func validRunID(value string) bool {
	return validIDPart(value) && !strings.Contains(value, string(filepath.Separator))
}

func ensureOutsideRepository(runDir, repository string) error {
	canonicalRun, err := canonicalForContainment(runDir)
	if err != nil {
		return err
	}
	canonicalRepository, err := canonicalForContainment(repository)
	if err != nil {
		return err
	}
	if pathWithin(canonicalRun, canonicalRepository) {
		return fmt.Errorf("%w: %q", ErrRunInsideRepo, canonicalRun)
	}
	return nil
}

func canonicalForContainment(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	current := absolute
	var suffix []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(current)
		if resolveErr == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(resolveErr, os.ErrNotExist) {
			return "", resolveErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func pathWithin(path, base string) bool {
	relative, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
