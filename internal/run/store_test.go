package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2026, time.August, 2, 12, 34, 56, 123, time.UTC)

type fakeClock struct{ value time.Time }

func (clock *fakeClock) Now() time.Time { return clock.value }

type fakeID string

func (id fakeID) NewID() (string, error) { return string(id), nil }

func TestStoreCreateSaveLoadAndArtifacts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repository := filepath.Join(root, "repo")
	cache := filepath.Join(root, "cache")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{value: testTime}
	store, err := NewStore(cache, WithClock(clock), WithIDGenerator(fakeID("abcdef123456")))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := store.Create(context.Background(), CreateOptions{
		Mode:       ModePlan,
		Repository: repository,
		PlanPath:   filepath.Join(repository, "REVIEW_SPEC.md"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "20260802T123456.000000123Z-abcdef123456" {
		t.Fatalf("unexpected run id %q", manifest.ID)
	}
	if manifest.Source != SourcePlanOnly {
		t.Fatalf("source = %q, want %q", manifest.Source, SourcePlanOnly)
	}
	if pathWithin(manifest.RunDir, repository) {
		t.Fatalf("run dir %q is inside repository %q", manifest.RunDir, repository)
	}
	for _, relative := range []string{
		"manifest.json", "git", "context/jira", "context/confluence", "context/bitbucket",
		"context/project-instructions", "packet", "candidates", "evidence",
	} {
		if _, err := os.Stat(filepath.Join(manifest.RunDir, relative)); err != nil {
			t.Errorf("expected run artifact path %q: %v", relative, err)
		}
	}

	artifactPath, err := store.WriteArtifact(context.Background(), manifest.ID, []byte("first"), "git", "diff.patch")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(artifactPath); err != nil || string(data) != "first" {
		t.Fatalf("artifact = %q, %v", data, err)
	}
	info, err := os.Stat(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode = %o, want 600", info.Mode().Perm())
	}

	clock.value = testTime.Add(time.Minute)
	manifest.State = StateRunning
	if err := manifest.SetStage("collect", StageRunning, clock.value, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background(), manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != StateRunning || !loaded.UpdatedAt.Equal(clock.value) {
		t.Fatalf("loaded manifest state/time = %q/%s", loaded.State, loaded.UpdatedAt)
	}
}

func TestStoreRejectsRunInsideRepository(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	store, err := NewStore(filepath.Join(repository, ".cache"), WithClock(&fakeClock{value: testTime}), WithIDGenerator(fakeID("safe")))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(context.Background(), CreateOptions{Mode: ModeImplementation, Repository: repository})
	if !errors.Is(err, ErrRunInsideRepo) {
		t.Fatalf("Create() error = %v, want ErrRunInsideRepo", err)
	}
}

func TestArtifactPathRejectsTraversalAndInvalidRunID(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ArtifactPath("../escape", "manifest.json"); !errors.Is(err, ErrInvalidRunID) {
		t.Fatalf("ArtifactPath() error = %v, want ErrInvalidRunID", err)
	}
	if _, err := store.ArtifactPath("valid", "..", "outside"); !errors.Is(err, ErrArtifactEscapes) {
		t.Fatalf("ArtifactPath() error = %v, want ErrArtifactEscapes", err)
	}
}

func TestCanceledArtifactWritePreservesExistingFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repository := filepath.Join(root, "repo")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(filepath.Join(root, "cache"), WithClock(&fakeClock{value: testTime}), WithIDGenerator(fakeID("cancel")))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := store.Create(context.Background(), CreateOptions{Mode: ModeImplementation, Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.WriteArtifact(context.Background(), manifest.ID, []byte("old"), "review.json")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.WriteArtifact(ctx, manifest.ID, []byte("new"), "review.json"); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteArtifact() error = %v, want context.Canceled", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Fatalf("canceled write replaced artifact with %q", data)
	}
}

func TestLoadRejectsUnknownManifestFields(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repository := filepath.Join(root, "repo")
	if err := os.Mkdir(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(filepath.Join(root, "cache"), WithClock(&fakeClock{value: testTime}), WithIDGenerator(fakeID("strict")))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := store.Create(context.Background(), CreateOptions{Mode: ModeImplementation, Repository: repository})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(manifest.RunDir, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := strings.Replace(string(data), "{", "{\n  \"surprise\": true,", 1)
	if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background(), manifest.ID); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load() error = %v, want unknown field", err)
	}
}

func TestDefaultRootUsesXDGCacheHome(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_CACHE_HOME", cache)
	root, err := DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(cache, "zephyr", "runs") {
		t.Fatalf("DefaultRoot() = %q", root)
	}
}
