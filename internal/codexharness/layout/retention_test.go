package layout_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/signaturekey/zephyr/internal/codexharness/layout"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultRetentionPolicyUsesDocumentedBounds(t *testing.T) {
	assert.Equal(t, layout.RetentionPolicy{
		OperationMaxAge: 14 * 24 * time.Hour,
		OperationMax:    50,
		CacheMaxAge:     30 * 24 * time.Hour,
		CacheMax:        8,
	}, layout.DefaultRetentionPolicy())
}

func TestPruneDeletesOnlyOwnedCompletedEntriesByAgeAndCount(t *testing.T) {
	root := canonicalTempDir(t)
	now := time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC)
	oldOperation := ownedEntry(t, filepath.Join(root, "operations"), "old", now.Add(-15*24*time.Hour))
	countOperation := ownedEntry(t, filepath.Join(root, "operations"), "count", now.Add(-2*time.Hour))
	keepOperation := ownedEntry(t, filepath.Join(root, "operations"), "keep", now.Add(-time.Hour))
	oldCache := ownedEntry(t, filepath.Join(root, "cache"), "old-cache", now.Add(-31*24*time.Hour))
	keepCache := ownedEntry(t, filepath.Join(root, "cache"), "keep-cache", now.Add(-time.Hour))
	policy := layout.RetentionPolicy{
		OperationMaxAge: 14 * 24 * time.Hour,
		OperationMax:    1,
		CacheMaxAge:     30 * 24 * time.Hour,
		CacheMax:        1,
	}

	_, err := layout.Prune(root, policy, now)
	require.NoError(t, err)

	assert.NoDirExists(t, oldOperation)
	assert.NoDirExists(t, countOperation)
	assert.DirExists(t, keepOperation)
	assert.NoDirExists(t, oldCache)
	assert.DirExists(t, keepCache)
}

func TestPruneSkipsSymlinkMalformedForeignAndPrivateEntries(t *testing.T) {
	root := canonicalTempDir(t)
	now := time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC)
	operations := filepath.Join(root, "operations")
	foreignName := "foreign-sk-test-secret"
	malformedName := "malformed-sk-test-secret"
	privateName := "private-sk-test-secret"
	linkName := "link-sk-test-secret"
	foreign := filepath.Join(operations, foreignName)
	require.NoError(t, os.MkdirAll(foreign, 0o700))
	malformed := filepath.Join(operations, malformedName)
	require.NoError(t, os.MkdirAll(malformed, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(malformed, layout.OwnerMarkerName), []byte("not-owned"), 0o600))
	private := ownedEntry(t, operations, privateName, now.Add(-90*24*time.Hour))
	require.NoError(t, os.Mkdir(filepath.Join(private, "private"), 0o700))
	target := ownedEntry(t, t.TempDir(), "outside", now.Add(-90*24*time.Hour))
	symlink := filepath.Join(operations, linkName)
	require.NoError(t, os.Symlink(target, symlink))

	events, err := layout.Prune(root, layout.DefaultRetentionPolicy(), now)
	require.NoError(t, err)

	assert.DirExists(t, foreign)
	assert.DirExists(t, malformed)
	assert.DirExists(t, private)
	info, err := os.Lstat(symlink)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
	assert.DirExists(t, target)
	reasons := make([]layout.CoverageReason, 0, len(events))
	for _, event := range events {
		reasons = append(reasons, event.Reason)
		assert.Regexp(t, `^[0-9a-f]{64}$`, event.EntrySHA256)
	}
	assert.ElementsMatch(t, []layout.CoverageReason{
		layout.CoverageForeignEntry,
		layout.CoverageMalformedMarker,
		layout.CoveragePrivateRetained,
		layout.CoverageSymlinkEntry,
	}, reasons)
	encoded, err := json.Marshal(events)
	require.NoError(t, err)
	for _, entryName := range []string{foreignName, malformedName, privateName, linkName} {
		assert.NotContains(t, string(encoded), entryName)
	}
}

func TestPruneRejectsSymlinkedRootComponentAndInvalidPolicy(t *testing.T) {
	realRoot := canonicalTempDir(t)
	linkRoot := filepath.Join(canonicalTempDir(t), "linked")
	require.NoError(t, os.Symlink(realRoot, linkRoot))

	_, err := layout.Prune(linkRoot, layout.DefaultRetentionPolicy(), time.Now())
	require.Error(t, err)
	policy := layout.DefaultRetentionPolicy()
	policy.OperationMax = -1
	_, err = layout.Prune(realRoot, policy, time.Now())
	require.Error(t, err)
}

func TestPruneRejectsSymlinkedManagedRootWithoutTouchingTarget(t *testing.T) {
	root := canonicalTempDir(t)
	target := t.TempDir()
	protected := ownedEntry(t, target, "protected", time.Now().Add(-90*24*time.Hour))
	require.NoError(t, os.Symlink(target, filepath.Join(root, "operations")))

	_, err := layout.Prune(root, layout.DefaultRetentionPolicy(), time.Now())

	require.Error(t, err)
	assert.DirExists(t, protected)
}

func TestPruneRejectsSymlinkedAncestorBeforeDeletingAnything(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	realParent := filepath.Join(base, "real")
	realRoot := filepath.Join(realParent, "root")
	require.NoError(t, os.MkdirAll(filepath.Join(realRoot, "operations"), 0o700))
	protected := ownedEntry(t, filepath.Join(realRoot, "operations"), "protected", time.Now().Add(-90*24*time.Hour))
	alias := filepath.Join(base, "alias")
	require.NoError(t, os.Symlink(realParent, alias))

	_, err = layout.Prune(filepath.Join(alias, "root"), layout.DefaultRetentionPolicy(), time.Now())

	require.Error(t, err)
	assert.DirExists(t, protected)
}

func ownedEntry(t *testing.T, parent, name string, modified time.Time) string {
	t.Helper()
	path := filepath.Join(parent, name)
	require.NoError(t, os.MkdirAll(path, 0o700))
	marker := filepath.Join(path, layout.OwnerMarkerName)
	require.NoError(t, os.WriteFile(marker, []byte(layout.OwnerMarkerText), 0o600))
	require.NoError(t, os.Chmod(marker, 0o600))
	if filepath.Base(parent) == "operations" {
		require.NoError(t, os.WriteFile(filepath.Join(path, "diagnostics.json"), []byte("{}\n"), 0o600))
	}
	require.NoError(t, os.Chtimes(path, modified, modified))
	return path
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return path
}
