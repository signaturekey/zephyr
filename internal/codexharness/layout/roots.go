package layout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrRootOverlap = errors.New("Zephyr driver roots overlap the reviewed repository")

type Roots struct {
	DriverRoot string
	Operation  string
	RunRoot    string
	CacheRoot  string
}

func ValidateManagedRoots(roots Roots) error {
	if !filepath.IsAbs(roots.DriverRoot) ||
		roots.Operation != filepath.Join(roots.DriverRoot, "operations") ||
		roots.RunRoot != filepath.Join(roots.DriverRoot, "runs") ||
		roots.CacheRoot != filepath.Join(roots.DriverRoot, "cache") {
		return errors.New("inconsistent Zephyr driver roots")
	}
	for name, path := range map[string]string{
		"driver": roots.DriverRoot, "operations": roots.Operation, "runs": roots.RunRoot, "cache": roots.CacheRoot,
	} {
		if err := validateNoSymlinkComponents(path); err != nil {
			return fmt.Errorf("validate %s root: %w", name, err)
		}
	}
	return nil
}

func Resolve(repository, configuredDriverRoot string) (Roots, error) {
	canonicalRepository, err := canonicalPath(repository)
	if err != nil {
		return Roots{}, fmt.Errorf("resolve repository: %w", err)
	}
	info, err := os.Stat(canonicalRepository)
	if err != nil {
		return Roots{}, fmt.Errorf("inspect repository %q: %w", canonicalRepository, err)
	}
	if !info.IsDir() {
		return Roots{}, fmt.Errorf("repository %q is not a directory", canonicalRepository)
	}

	driverRoot := strings.TrimSpace(configuredDriverRoot)
	if driverRoot == "" {
		driverRoot, err = defaultDriverRoot()
		if err != nil {
			return Roots{}, err
		}
	}
	resolved, err := resolveCandidate(canonicalRepository, driverRoot)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, ErrRootOverlap) {
		return Roots{}, err
	}

	tempBase, tempErr := canonicalPath(os.TempDir())
	if tempErr != nil {
		return Roots{}, fmt.Errorf("prove temporary directory safety: %w", tempErr)
	}
	if pathsOverlap(canonicalRepository, tempBase) {
		return Roots{}, fmt.Errorf("temporary directory %q intersects repository %q: %w", tempBase, canonicalRepository, ErrRootOverlap)
	}
	fallback, tempErr := os.MkdirTemp(tempBase, "zephyr-codex-")
	if tempErr != nil {
		return Roots{}, fmt.Errorf("create safe fallback driver root: %w", tempErr)
	}
	if chmodErr := os.Chmod(fallback, 0o700); chmodErr != nil {
		_ = os.Remove(fallback)
		return Roots{}, fmt.Errorf("secure fallback driver root: %w", chmodErr)
	}
	resolved, err = resolveCandidate(canonicalRepository, fallback)
	if err != nil {
		_ = os.Remove(fallback)
		return Roots{}, fmt.Errorf("validate fallback driver root: %w", err)
	}
	return resolved, nil
}

func defaultDriverRoot() (string, error) {
	if cache := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); cache != "" {
		if !filepath.IsAbs(cache) {
			return "", errors.New("XDG_CACHE_HOME must be absolute")
		}
		return filepath.Join(cache, "zephyr", "codex"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "zephyr", "codex"), nil
}

func resolveCandidate(repository, driverRoot string) (Roots, error) {
	driver, err := canonicalPath(driverRoot)
	if err != nil {
		return Roots{}, fmt.Errorf("resolve driver root: %w", err)
	}
	roots := Roots{
		DriverRoot: driver,
		Operation:  filepath.Join(driver, "operations"),
		RunRoot:    filepath.Join(driver, "runs"),
		CacheRoot:  filepath.Join(driver, "cache"),
	}
	for name, candidate := range map[string]string{
		"driver":     roots.DriverRoot,
		"operations": roots.Operation,
		"runs":       roots.RunRoot,
		"cache":      roots.CacheRoot,
	} {
		canonicalCandidate, err := canonicalPath(candidate)
		if err != nil {
			return Roots{}, fmt.Errorf("resolve %s root: %w", name, err)
		}
		if pathsOverlap(repository, canonicalCandidate) {
			return Roots{}, fmt.Errorf("%s root %q intersects repository %q: %w", name, canonicalCandidate, repository, ErrRootOverlap)
		}
	}
	return roots, nil
}

func canonicalPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute: %q", path)
	}
	clean := filepath.Clean(path)
	existing := clean
	var suffix []string
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("no existing ancestor for %q", path)
		}
		suffix = append(suffix, filepath.Base(existing))
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	for index := len(suffix) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, suffix[index])
	}
	return filepath.Clean(resolved), nil
}

func pathsOverlap(left, right string) bool {
	return containsPath(left, right) || containsPath(right, left)
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func validateNoSymlinkComponents(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("path must be absolute")
	}
	current := filepath.Clean(path)
	paths := []string{current}
	for {
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		paths = append(paths, parent)
		current = parent
	}
	for index := len(paths) - 1; index >= 0; index-- {
		info, err := os.Lstat(paths[index])
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink component %q is not allowed", paths[index])
		}
		if !info.IsDir() {
			return fmt.Errorf("path component %q is not a directory", paths[index])
		}
	}
	return nil
}
