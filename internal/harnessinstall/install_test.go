package harnessinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstallCodex(t *testing.T) {
	root := resolvedTempDir(t)
	result, err := Install(Options{
		CodexSkillsDir: filepath.Join(root, "codex-skills"),
		CodexAgentsDir: filepath.Join(root, "codex-agents"),
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Files, "installation changed no files")
	for _, path := range []string{
		filepath.Join(root, "codex-skills", "zephyr", "SKILL.md"),
		filepath.Join(root, "codex-skills", "zephyr", "scripts", "dispatch.sh"),
		filepath.Join(root, "codex-skills", "zephyr", "agents", "openai.yaml"),
		filepath.Join(root, "codex-agents", "zephyr-code-reviewer.toml"),
	} {
		_, err := os.Stat(path)
		require.NoErrorf(t, err, "installed file %s", path)
	}
}

func TestInstallRejectsDifferentFileBeforeWriting(t *testing.T) {
	root := resolvedTempDir(t)
	conflict := filepath.Join(root, "codex-skills", "zephyr", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(conflict), 0o700))
	require.NoError(t, os.WriteFile(conflict, []byte("foreign"), 0o600))
	_, err := Install(Options{
		CodexSkillsDir: filepath.Join(root, "codex-skills"),
		CodexAgentsDir: filepath.Join(root, "codex-agents"),
	})
	require.Error(t, err, "installation unexpectedly replaced a different file")
	_, err = os.Stat(filepath.Join(root, "codex-agents"))
	assert.True(t, os.IsNotExist(err), "preflight wrote agent directory: %v", err)
}

func TestUninstallCodex(t *testing.T) {
	root := resolvedTempDir(t)
	options := Options{
		CodexSkillsDir: filepath.Join(root, "codex-skills"),
		CodexAgentsDir: filepath.Join(root, "codex-agents"),
	}
	_, err := Install(options)
	require.NoError(t, err)
	result, err := Uninstall(options)
	require.NoError(t, err)
	require.NotEmpty(t, result.Files, "uninstall removed no files")
	for _, path := range []string{
		filepath.Join(root, "codex-skills", "zephyr", "SKILL.md"),
		filepath.Join(root, "codex-agents", "zephyr-code-reviewer.toml"),
	} {
		_, err := os.Stat(path)
		assert.Truef(t, os.IsNotExist(err), "uninstalled file still exists %s: %v", path, err)
	}
}

func TestUninstallAcceptsHistoricalInstalledManifest(t *testing.T) {
	root := resolvedTempDir(t)
	options := Options{
		CodexSkillsDir: filepath.Join(root, "codex-skills"),
		CodexAgentsDir: filepath.Join(root, "codex-agents"),
	}
	_, err := Install(options)
	require.NoError(t, err)
	manifest := filepath.Join(root, "codex-skills", "zephyr", "references", "assets.sha256")
	content, err := os.ReadFile(manifest)
	require.NoError(t, err)
	lines := strings.Split(string(content), "\n")
	for index, line := range lines {
		if strings.HasSuffix(line, "  harnesses/assets.sha256") {
			lines[index] = strings.Repeat("0", 64) + "  harnesses/assets.sha256"
			break
		}
	}
	require.NoError(t, os.WriteFile(manifest, []byte(strings.Join(lines, "\n")), 0o600))
	_, err = Uninstall(options)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "codex-skills", "zephyr", "SKILL.md"))
	assert.True(t, os.IsNotExist(err), "historical skill was not removed: %v", err)
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return root
}
