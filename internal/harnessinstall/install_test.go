package harnessinstall

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallAll(t *testing.T) {
	root := resolvedTempDir(t)
	result, err := Install(Options{
		Surface:         SurfaceAll,
		CodexSkillsDir:  filepath.Join(root, "codex-skills"),
		CodexAgentsDir:  filepath.Join(root, "codex-agents"),
		ClaudeSkillsDir: filepath.Join(root, "claude-skills"),
		ClaudeAgentsDir: filepath.Join(root, "claude-agents"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) == 0 {
		t.Fatal("installation changed no files")
	}
	for _, path := range []string{
		filepath.Join(root, "codex-skills", "zephyr", "SKILL.md"),
		filepath.Join(root, "codex-skills", "zephyr", "scripts", "dispatch.sh"),
		filepath.Join(root, "claude-skills", "zephyr", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("installed file %s: %v", path, err)
		}
	}
}

func TestInstallRejectsDifferentFileBeforeWriting(t *testing.T) {
	root := resolvedTempDir(t)
	conflict := filepath.Join(root, "codex-skills", "zephyr", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(conflict), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflict, []byte("foreign"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Install(Options{
		Surface:        SurfaceCodex,
		CodexSkillsDir: filepath.Join(root, "codex-skills"),
		CodexAgentsDir: filepath.Join(root, "codex-agents"),
	})
	if err == nil {
		t.Fatal("installation unexpectedly replaced a different file")
	}
	if _, err := os.Stat(filepath.Join(root, "codex-agents")); !os.IsNotExist(err) {
		t.Fatalf("preflight wrote agent directory: %v", err)
	}
}

func resolvedTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
