package harnessinstall

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	zephyrassets "github.com/signaturekey/zephyr"
)

type Surface string

const (
	SurfaceCodex  Surface = "codex"
	SurfaceClaude Surface = "claude"
	SurfaceAll    Surface = "all"
)

type Options struct {
	Surface         Surface
	CodexSkillsDir  string
	CodexAgentsDir  string
	ClaudeSkillsDir string
	ClaudeAgentsDir string
}

type Result struct {
	Surface Surface  `json:"surface"`
	Files   []string `json:"files"`
	Message string   `json:"message"`
}

type asset struct {
	source      string
	destination string
	mode        fs.FileMode
}

func OptionsFromEnvironment(surface Surface) (Options, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Options{}, fmt.Errorf("resolve home directory: %w", err)
	}
	return Options{
		Surface:         surface,
		CodexSkillsDir:  environmentOr("ZEPHYR_CODEX_SKILLS_DIR", filepath.Join(home, ".agents", "skills")),
		CodexAgentsDir:  environmentOr("ZEPHYR_CODEX_AGENTS_DIR", filepath.Join(home, ".codex", "agents")),
		ClaudeSkillsDir: environmentOr("ZEPHYR_CLAUDE_SKILLS_DIR", filepath.Join(home, ".claude", "skills")),
		ClaudeAgentsDir: environmentOr("ZEPHYR_CLAUDE_AGENTS_DIR", filepath.Join(home, ".claude", "agents")),
	}, nil
}

func Install(options Options) (Result, error) {
	if options.Surface != SurfaceCodex && options.Surface != SurfaceClaude && options.Surface != SurfaceAll {
		return Result{}, fmt.Errorf("unsupported harness surface %q", options.Surface)
	}
	if err := verifyManifest(); err != nil {
		return Result{}, err
	}
	assets, err := installationAssets(options)
	if err != nil {
		return Result{}, err
	}
	for _, item := range assets {
		if err := preflight(item); err != nil {
			return Result{}, err
		}
	}
	installed := make([]string, 0, len(assets))
	for _, item := range assets {
		changed, err := installAsset(item)
		if err != nil {
			return Result{}, err
		}
		if changed {
			installed = append(installed, item.destination)
		}
	}
	return Result{
		Surface: options.Surface,
		Files:   installed,
		Message: "Начните новую сессию harness, чтобы загрузились установленный skill и agents.",
	}, nil
}

func environmentOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func installationAssets(options Options) ([]asset, error) {
	var result []asset
	if options.Surface == SurfaceCodex || options.Surface == SurfaceAll {
		items, err := codexAssets(options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	if options.Surface == SurfaceClaude || options.Surface == SurfaceAll {
		items, err := claudeAssets(options)
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
	}
	return result, nil
}

func codexAssets(options Options) ([]asset, error) {
	skillRoot, err := secureRoot(options.CodexSkillsDir, "zephyr")
	if err != nil {
		return nil, err
	}
	agentsRoot, err := secureRoot(options.CodexAgentsDir)
	if err != nil {
		return nil, err
	}
	result := []asset{
		mapping("harnesses/codex/SKILL.md", filepath.Join(skillRoot, "SKILL.md"), 0o600),
		mapping("harnesses/codex/dispatch.sh", filepath.Join(skillRoot, "scripts", "dispatch.sh"), 0o700),
		mapping("harnesses/codex/discovery/agents/openai.yaml", filepath.Join(skillRoot, "agents", "openai.yaml"), 0o600),
		mapping("harnesses/assets.sha256", filepath.Join(skillRoot, "references", "assets.sha256"), 0o600),
	}
	result, err = appendGroup(result, "roles/*.md", filepath.Join(skillRoot, "references", "roles"), 0o600)
	if err != nil {
		return nil, err
	}
	result, err = appendGroup(result, "schemas/*.json", filepath.Join(skillRoot, "references", "schemas"), 0o600)
	if err != nil {
		return nil, err
	}
	agentSources, err := fs.Glob(zephyrassets.Harness, "harnesses/codex/agents/zephyr-*.toml")
	if err != nil {
		return nil, fmt.Errorf("list embedded Codex agents: %w", err)
	}
	for _, source := range agentSources {
		name := filepath.Base(source)
		result = append(result,
			mapping(source, filepath.Join(agentsRoot, name), 0o600),
			mapping(source, filepath.Join(skillRoot, "references", "agents", name), 0o600),
		)
	}
	return result, nil
}

func claudeAssets(options Options) ([]asset, error) {
	skillRoot, err := secureRoot(options.ClaudeSkillsDir, "zephyr")
	if err != nil {
		return nil, err
	}
	agentsRoot, err := secureRoot(options.ClaudeAgentsDir)
	if err != nil {
		return nil, err
	}
	result := []asset{
		mapping("harnesses/claude-code/SKILL.md", filepath.Join(skillRoot, "SKILL.md"), 0o600),
		mapping("harnesses/assets.sha256", filepath.Join(skillRoot, "references", "assets.sha256"), 0o600),
	}
	result, err = appendGroup(result, "roles/*.md", filepath.Join(skillRoot, "references", "roles"), 0o600)
	if err != nil {
		return nil, err
	}
	result, err = appendGroup(result, "schemas/*.json", filepath.Join(skillRoot, "references", "schemas"), 0o600)
	if err != nil {
		return nil, err
	}
	agentSources, err := fs.Glob(zephyrassets.Harness, "harnesses/claude-code/agents/zephyr-*.md")
	if err != nil {
		return nil, fmt.Errorf("list embedded Claude agents: %w", err)
	}
	for _, source := range agentSources {
		name := filepath.Base(source)
		result = append(result,
			mapping(source, filepath.Join(agentsRoot, name), 0o600),
			mapping(source, filepath.Join(skillRoot, "references", "agents", name), 0o600),
		)
	}
	return result, nil
}

func appendGroup(result []asset, pattern, destinationRoot string, mode fs.FileMode) ([]asset, error) {
	sources, err := fs.Glob(zephyrassets.Harness, pattern)
	if err != nil {
		return nil, fmt.Errorf("list embedded assets %q: %w", pattern, err)
	}
	for _, source := range sources {
		result = append(result, mapping(source, filepath.Join(destinationRoot, filepath.Base(source)), mode))
	}
	return result, nil
}

func mapping(source, destination string, mode fs.FileMode) asset {
	return asset{source: source, destination: destination, mode: mode}
}

func secureRoot(parts ...string) (string, error) {
	root := filepath.Join(parts...)
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("installation target must be absolute: %s", root)
	}
	cleaned := filepath.Clean(root)
	if cleaned == string(filepath.Separator) {
		return "", errors.New("installation target must not be filesystem root")
	}
	return cleaned, nil
}

func verifyManifest() error {
	manifest, err := zephyrassets.Harness.ReadFile("harnesses/assets.sha256")
	if err != nil {
		return fmt.Errorf("read embedded asset manifest: %w", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(manifest))
	seen := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return fmt.Errorf("invalid embedded asset manifest line %q", scanner.Text())
		}
		expected, err := hex.DecodeString(fields[0])
		if err != nil || len(expected) != sha256.Size {
			return fmt.Errorf("invalid embedded asset hash for %s", fields[1])
		}
		content, err := zephyrassets.Harness.ReadFile(fields[1])
		if err != nil {
			return fmt.Errorf("read embedded asset %s: %w", fields[1], err)
		}
		actual := sha256.Sum256(content)
		if !bytes.Equal(expected, actual[:]) {
			return fmt.Errorf("embedded asset checksum mismatch: %s", fields[1])
		}
		seen++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read embedded asset manifest: %w", err)
	}
	if seen == 0 {
		return errors.New("embedded asset manifest is empty")
	}
	return nil
}

func preflight(item asset) error {
	content, err := zephyrassets.Harness.ReadFile(item.source)
	if err != nil {
		return fmt.Errorf("read embedded asset %s: %w", item.source, err)
	}
	if err := rejectSymlinkComponents(filepath.Dir(item.destination)); err != nil {
		return err
	}
	info, err := os.Lstat(item.destination)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect installation target %s: %w", item.destination, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to overwrite non-regular file: %s", item.destination)
	}
	existing, err := os.ReadFile(item.destination)
	if err != nil {
		return fmt.Errorf("read installation target %s: %w", item.destination, err)
	}
	if !bytes.Equal(content, existing) {
		return fmt.Errorf("refusing to overwrite different file: %s", item.destination)
	}
	return nil
}

func installAsset(item asset) (bool, error) {
	if _, err := os.Lstat(item.destination); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect installation target %s: %w", item.destination, err)
	}
	content, err := zephyrassets.Harness.ReadFile(item.source)
	if err != nil {
		return false, fmt.Errorf("read embedded asset %s: %w", item.source, err)
	}
	parent := filepath.Dir(item.destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return false, fmt.Errorf("create installation directory %s: %w", parent, err)
	}
	if err := rejectSymlinkComponents(parent); err != nil {
		return false, err
	}
	temporary, err := os.CreateTemp(parent, ".zephyr-install.*")
	if err != nil {
		return false, fmt.Errorf("create temporary installation file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(item.mode); err != nil {
		temporary.Close()
		return false, fmt.Errorf("set installation file mode: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return false, fmt.Errorf("write installation file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return false, fmt.Errorf("sync installation file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("close installation file: %w", err)
	}
	if err := os.Link(temporaryName, item.destination); err != nil {
		return false, fmt.Errorf("publish installation file %s: %w", item.destination, err)
	}
	return true, nil
}

func rejectSymlinkComponents(path string) error {
	path = filepath.Clean(path)
	var components []string
	for current := path; current != string(filepath.Separator); current = filepath.Dir(current) {
		components = append(components, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	sort.Slice(components, func(left, right int) bool { return len(components[left]) < len(components[right]) })
	for _, component := range components {
		info, err := os.Lstat(component)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect installation path %s: %w", component, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing installation target with symlink component: %s", component)
		}
	}
	return nil
}
