package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, os.Mkdir(repository, 0o700))
	clock := &fakeClock{value: testTime}
	store, err := NewStore(cache, WithClock(clock), WithIDGenerator(fakeID("abcdef123456")))
	require.NoError(t, err)
	manifest, err := store.Create(context.Background(), CreateOptions{
		Mode:       ModePlan,
		Repository: repository,
		PlanPath:   filepath.Join(repository, "REVIEW_SPEC.md"),
	})
	require.NoError(t, err)
	assert.Equal(t, "20260802T123456.000000123Z-abcdef123456", manifest.ID)
	assert.Equal(t, SourcePlanOnly, manifest.Source)
	assert.False(t, pathWithin(manifest.RunDir, repository), "run dir %q is inside repository %q", manifest.RunDir, repository)
	for _, relative := range []string{
		"manifest.json", "git", "context/jira", "context/confluence", "context/bitbucket",
		"context/project-instructions", "packet", "candidates", "evidence",
	} {
		_, err := os.Stat(filepath.Join(manifest.RunDir, relative))
		assert.NoErrorf(t, err, "expected run artifact path %q", relative)
	}

	artifactPath, err := store.WriteArtifact(context.Background(), manifest.ID, []byte("first"), "git", "diff.patch")
	require.NoError(t, err)
	data, err := os.ReadFile(artifactPath)
	require.NoError(t, err)
	assert.Equal(t, "first", string(data))
	info, err := os.Stat(artifactPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	clock.value = testTime.Add(time.Minute)
	manifest.State = StateRunning
	require.NoError(t, manifest.SetStage("collect", StageRunning, clock.value, ""))
	require.NoError(t, store.Save(context.Background(), manifest))
	loaded, err := store.Load(context.Background(), manifest.ID)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, loaded.State)
	assert.True(t, loaded.UpdatedAt.Equal(clock.value))
}

func TestStoreRejectsRunInsideRepository(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	store, err := NewStore(filepath.Join(repository, ".cache"), WithClock(&fakeClock{value: testTime}), WithIDGenerator(fakeID("safe")))
	require.NoError(t, err)
	_, err = store.Create(context.Background(), CreateOptions{Mode: ModeImplementation, Repository: repository})
	require.ErrorIs(t, err, ErrRunInsideRepo)
}

func TestArtifactPathRejectsTraversalAndInvalidRunID(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	require.NoError(t, err)
	_, err = store.ArtifactPath("../escape", "manifest.json")
	require.ErrorIs(t, err, ErrInvalidRunID)
	_, err = store.ArtifactPath("valid", "..", "outside")
	require.ErrorIs(t, err, ErrArtifactEscapes)
}

func TestCanceledArtifactWritePreservesExistingFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repository := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repository, 0o700))
	store, err := NewStore(filepath.Join(root, "cache"), WithClock(&fakeClock{value: testTime}), WithIDGenerator(fakeID("cancel")))
	require.NoError(t, err)
	manifest, err := store.Create(context.Background(), CreateOptions{Mode: ModeImplementation, Repository: repository})
	require.NoError(t, err)
	path, err := store.WriteArtifact(context.Background(), manifest.ID, []byte("old"), "review.json")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.WriteArtifact(ctx, manifest.ID, []byte("new"), "review.json")
	require.ErrorIs(t, err, context.Canceled)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "old", string(data))
}

func TestLoadRejectsUnknownManifestFields(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	repository := filepath.Join(root, "repo")
	require.NoError(t, os.Mkdir(repository, 0o700))
	store, err := NewStore(filepath.Join(root, "cache"), WithClock(&fakeClock{value: testTime}), WithIDGenerator(fakeID("strict")))
	require.NoError(t, err)
	manifest, err := store.Create(context.Background(), CreateOptions{Mode: ModeImplementation, Repository: repository})
	require.NoError(t, err)
	path := filepath.Join(manifest.RunDir, "manifest.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	corrupt := strings.Replace(string(data), "{", "{\n  \"surprise\": true,", 1)
	require.NoError(t, os.WriteFile(path, []byte(corrupt), 0o600))
	_, err = store.Load(context.Background(), manifest.ID)
	require.Error(t, err)
	assert.ErrorContains(t, err, "unknown field")
}

func TestDefaultRootUsesXDGCacheHome(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_CACHE_HOME", cache)
	root, err := DefaultRoot()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(cache, "zephyr", "runs"), root)
}
