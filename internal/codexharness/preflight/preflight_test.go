package preflight

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveExecutableIdentityRejectsGroupOrWorldWritable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool")
	require.NoError(t, os.WriteFile(path, []byte("tool-v1"), 0o755))
	require.NoError(t, os.Chmod(path, 0o775))

	_, err := resolveExecutableIdentity(path, "tool", nil)

	require.Error(t, err)
	assert.ErrorContains(t, err, "group or world writable")
}

func TestResolveExecutableIdentityReturnsStableFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool")
	content := []byte("tool-v1")
	require.NoError(t, os.WriteFile(path, content, 0o700))
	want := sha256.Sum256(content)
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)

	identity, err := resolveExecutableIdentity(path, "tool", nil)

	require.NoError(t, err)
	assert.Equal(t, resolved, identity.Path)
	assert.Equal(t, hex.EncodeToString(want[:]), identity.SHA256)
}

func TestVerifyExecutableIdentityRejectsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool")
	require.NoError(t, os.WriteFile(path, []byte("tool-v1"), 0o700))
	identity, err := resolveExecutableIdentity(path, "tool", nil)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("tool-v2"), 0o700))

	err = verifyExecutableIdentity(identity, "tool")

	require.Error(t, err)
	assert.ErrorContains(t, err, "changed after validation")
}
