package harnessinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCodex(t *testing.T) {
	root := resolvedTempDir(t)
	result, err := Install(Options{
		CodexSkillsDir: filepath.Join(root, "codex-skills"),
		CodexAgentsDir: filepath.Join(root, "codex-agents"),
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
		filepath.Join(root, "codex-skills", "zephyr", "agents", "openai.yaml"),
		filepath.Join(root, "codex-agents", "zephyr-code-reviewer.toml"),
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

func TestUninstallCodex(t *testing.T) {
	root := resolvedTempDir(t)
	options := Options{
		CodexSkillsDir: filepath.Join(root, "codex-skills"),
		CodexAgentsDir: filepath.Join(root, "codex-agents"),
	}
	if _, err := Install(options); err != nil {
		t.Fatal(err)
	}
	result, err := Uninstall(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) == 0 {
		t.Fatal("uninstall removed no files")
	}
	for _, path := range []string{
		filepath.Join(root, "codex-skills", "zephyr", "SKILL.md"),
		filepath.Join(root, "codex-agents", "zephyr-code-reviewer.toml"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("uninstalled file still exists %s: %v", path, err)
		}
	}
}

func TestUninstallAcceptsHistoricalInstalledManifest(t *testing.T) {
	root := resolvedTempDir(t)
	options := Options{
		CodexSkillsDir: filepath.Join(root, "codex-skills"),
		CodexAgentsDir: filepath.Join(root, "codex-agents"),
	}
	if _, err := Install(options); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "codex-skills", "zephyr", "references", "assets.sha256")
	content, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(content), "\n")
	for index, line := range lines {
		if strings.HasSuffix(line, "  harnesses/assets.sha256") {
			lines[index] = strings.Repeat("0", 64) + "  harnesses/assets.sha256"
			break
		}
	}
	if err := os.WriteFile(manifest, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "codex-skills", "zephyr", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("historical skill was not removed: %v", err)
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
