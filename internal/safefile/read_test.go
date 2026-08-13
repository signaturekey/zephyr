package safefile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadBeneathRejectsEscapesSymlinksAndLargeFiles(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(outside, []byte("sentinel"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "regular"), []byte("hello"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	data, err := ReadBeneath(root, "regular", 5)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
	_, err = ReadBeneath(root, "../secret", 100)
	require.ErrorIs(t, err, ErrEscapesRoot)
	_, err = ReadBeneath(root, "link", 100)
	require.ErrorIs(t, err, ErrSymlink)
	_, err = ReadBeneath(root, "regular", 4)
	require.ErrorIs(t, err, ErrTooLarge)
}
