package layout_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signaturekey/zephyr/internal/codexharness/layout"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveUsesSafeSiblingWithoutTouchingRepository(t *testing.T) {
	base := t.TempDir()
	repository := makeRepositoryFixture(t, filepath.Join(base, "repository"))
	driver := filepath.Join(base, "driver")
	before := repositorySnapshot(t, repository)

	roots, err := layout.Resolve(repository, driver)

	require.NoError(t, err)
	canonicalBase, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)
	canonicalDriver := filepath.Join(canonicalBase, "driver")
	assert.Equal(t, canonicalDriver, roots.DriverRoot)
	assert.Equal(t, filepath.Join(canonicalDriver, "operations"), roots.Operation)
	assert.Equal(t, filepath.Join(canonicalDriver, "runs"), roots.RunRoot)
	assert.Equal(t, filepath.Join(canonicalDriver, "cache"), roots.CacheRoot)
	assert.Equal(t, before, repositorySnapshot(t, repository))
}

func TestResolveFallsBackWhenConfiguredXDGRootIsInsideRepository(t *testing.T) {
	base := t.TempDir()
	repository := makeRepositoryFixture(t, filepath.Join(base, "repository"))
	safeTemp := filepath.Join(base, "safe-temp")
	require.NoError(t, os.Mkdir(safeTemp, 0o700))
	t.Setenv("TMPDIR", safeTemp)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(repository, ".cache"))
	before := repositorySnapshot(t, repository)

	roots, err := layout.Resolve(repository, "")

	require.NoError(t, err)
	assertSamePath(t, safeTemp, filepath.Dir(roots.DriverRoot))
	assert.Contains(t, filepath.Base(roots.DriverRoot), "zephyr-codex-")
	assertMode(t, roots.DriverRoot, 0o700)
	assert.Equal(t, before, repositorySnapshot(t, repository))
}

func TestResolveFallsBackWhenRepositoryEqualsHome(t *testing.T) {
	base := t.TempDir()
	repository := makeRepositoryFixture(t, filepath.Join(base, "home"))
	safeTemp := filepath.Join(base, "safe-temp")
	require.NoError(t, os.Mkdir(safeTemp, 0o700))
	t.Setenv("HOME", repository)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("TMPDIR", safeTemp)

	roots, err := layout.Resolve(repository, "")

	require.NoError(t, err)
	assertSamePath(t, safeTemp, filepath.Dir(roots.DriverRoot))
}

func TestResolveRejectsWithoutArtifactWhenRepositoryContainsTempBase(t *testing.T) {
	base := t.TempDir()
	repository := makeRepositoryFixture(t, filepath.Join(base, "repository"))
	tempBase := filepath.Join(repository, "tmp")
	require.NoError(t, os.Mkdir(tempBase, 0o700))
	t.Setenv("TMPDIR", tempBase)
	configured := filepath.Join(repository, ".driver")
	before := repositorySnapshot(t, repository)

	_, err := layout.Resolve(repository, configured)

	require.Error(t, err)
	assert.ErrorIs(t, err, layout.ErrRootOverlap)
	assert.Empty(t, matchingNames(t, tempBase, "zephyr-codex-"))
	assert.Equal(t, before, repositorySnapshot(t, repository))
}

func TestResolveCanonicalizesSymlinkedExistingAncestors(t *testing.T) {
	base := t.TempDir()
	realParent := filepath.Join(base, "real")
	repository := makeRepositoryFixture(t, filepath.Join(realParent, "repository"))
	link := filepath.Join(base, "linked")
	require.NoError(t, os.Symlink(realParent, link))
	safeTemp := filepath.Join(base, "safe-temp")
	require.NoError(t, os.Mkdir(safeTemp, 0o700))
	t.Setenv("TMPDIR", safeTemp)

	roots, err := layout.Resolve(repository, filepath.Join(link, "repository", "driver"))

	require.NoError(t, err)
	assertSamePath(t, safeTemp, filepath.Dir(roots.DriverRoot))
}

func TestResolveRejectsRelativeRepositoryAndDriverRoot(t *testing.T) {
	_, err := layout.Resolve("relative/repository", "/absolute/driver")
	require.Error(t, err)
	_, err = layout.Resolve(t.TempDir(), "relative/driver")
	require.Error(t, err)
}

func makeRepositoryFixture(t *testing.T, path string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("tracked\n"), 0o600))
	return path
}

func repositorySnapshot(t *testing.T, repository string) map[string]string {
	t.Helper()
	result := map[string]string{}
	require.NoError(t, filepath.Walk(repository, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		relative, err := filepath.Rel(repository, path)
		require.NoError(t, err)
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			result[relative] = string(data)
		} else {
			result[relative] = info.Mode().String()
		}
		return nil
	}))
	return result
}

func matchingNames(t *testing.T, root, prefix string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	var result []string
	for _, entry := range entries {
		if len(entry.Name()) >= len(prefix) && entry.Name()[:len(prefix)] == prefix {
			result = append(result, entry.Name())
		}
	}
	return result
}

func assertMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, expected, info.Mode().Perm())
}

func assertSamePath(t *testing.T, expected, actual string) {
	t.Helper()
	canonicalExpected, err := filepath.EvalSymlinks(expected)
	require.NoError(t, err)
	canonicalActual, err := filepath.EvalSymlinks(actual)
	require.NoError(t, err)
	assert.Equal(t, canonicalExpected, canonicalActual)
}
