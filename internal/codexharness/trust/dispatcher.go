package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Dispatcher struct {
	Path   string
	SHA256 string
}

func VerifyDispatcher(path string, assets fs.FS) (Dispatcher, error) {
	if !filepath.IsAbs(path) {
		return Dispatcher{}, errors.New("dispatcher path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Dispatcher{}, fmt.Errorf("inspect dispatcher: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Dispatcher{}, errors.New("dispatcher must be a regular non-symlink file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return Dispatcher{}, errors.New("dispatcher must not be group or world writable")
	}
	if info.Mode().Perm()&0o111 == 0 {
		return Dispatcher{}, errors.New("dispatcher is not executable")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return Dispatcher{}, errors.New("dispatcher is not owned by the effective user")
	}
	manifest, err := fs.ReadFile(assets, "harnesses/assets.sha256")
	if err != nil {
		return Dispatcher{}, fmt.Errorf("read embedded asset manifest: %w", err)
	}
	var expected string
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "harnesses/codex/dispatch.sh" {
			expected = fields[0]
			break
		}
	}
	if len(expected) != 64 {
		return Dispatcher{}, errors.New("embedded dispatcher digest is missing")
	}
	actual, err := HashRegular(path, false)
	if err != nil {
		return Dispatcher{}, err
	}
	if actual != expected {
		return Dispatcher{}, errors.New("dispatcher digest does not match embedded manifest")
	}
	return Dispatcher{Path: filepath.Clean(path), SHA256: actual}, nil
}

func HashRegular(path string, resolve bool) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	if resolve {
		var err error
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", path, err)
		}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("path must be a regular non-symlink file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
