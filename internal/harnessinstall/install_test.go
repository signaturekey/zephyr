package harnessinstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAll(t *testing.T) {
	root := resolvedTempDir(t)
	result, err := Install(Options{
		Surface:           SurfaceAll,
		CodexSkillsDir:    filepath.Join(root, "codex-skills"),
		CodexAgentsDir:    filepath.Join(root, "codex-agents"),
		ClaudeSkillsDir:   filepath.Join(root, "claude-skills"),
		ClaudeAgentsDir:   filepath.Join(root, "claude-agents"),
		OpenCodeSkillsDir: filepath.Join(root, "opencode-skills"),
		OpenCodeAgentsDir: filepath.Join(root, "opencode-agents"),
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
		filepath.Join(root, "opencode-skills", "zephyr", "SKILL.md"),
		filepath.Join(root, "opencode-skills", "zephyr", "scripts", "dispatch.sh"),
		filepath.Join(root, "opencode-agents", "zephyr-code-reviewer.md"),
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

func TestUninstallOpenCode(t *testing.T) {
	root := resolvedTempDir(t)
	options := Options{
		Surface:           SurfaceOpenCode,
		OpenCodeSkillsDir: filepath.Join(root, "opencode-skills"),
		OpenCodeAgentsDir: filepath.Join(root, "opencode-agents"),
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
		filepath.Join(root, "opencode-skills", "zephyr", "SKILL.md"),
		filepath.Join(root, "opencode-agents", "zephyr-code-reviewer.md"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("uninstalled file still exists %s: %v", path, err)
		}
	}
}

func TestUninstallAcceptsHistoricalInstalledManifest(t *testing.T) {
	root := resolvedTempDir(t)
	options := Options{
		Surface:        SurfaceCodex,
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

func TestOpenCodeAgentDefinition(t *testing.T) {
	root := resolvedTempDir(t)
	options := Options{
		Surface:           SurfaceOpenCode,
		OpenCodeSkillsDir: filepath.Join(root, "opencode-skills"),
		OpenCodeAgentsDir: filepath.Join(root, "opencode-agents"),
	}
	if _, err := Install(options); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "opencode-agents", "zephyr-code-reviewer.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"mode: subagent",
		"permission:\n  '*': deny",
		"# Role: code-reviewer",
	} {
		if !strings.Contains(string(content), expected) {
			t.Fatalf("OpenCode agent definition is missing %q", expected)
		}
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
