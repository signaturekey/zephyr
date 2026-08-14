package compatibility

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCacheStore_FirstMissPersistsDescriptor(t *testing.T) {
	cache, err := NewCache(t.TempDir())
	require.NoError(t, err)
	key := Key{
		CodexBinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ModelPolicySHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		DispatcherSHA256:  "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	descriptor := []byte("zephyr-codex-compat-v3\nprofile=full\n")

	path, err := cache.Store(context.Background(), key, descriptor)
	require.NoError(t, err)
	require.FileExists(t, path)

	loaded, loadedPath, err := cache.Load(key)
	require.NoError(t, err)
	require.Equal(t, path, loadedPath)
	require.Equal(t, descriptor, loaded)
}
