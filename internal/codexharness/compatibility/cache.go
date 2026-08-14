package compatibility

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/signaturekey/zephyr/internal/codexharness/layout"
)

type Key struct{ CodexBinarySHA256, ModelPolicySHA256, DispatcherSHA256 string }

func (k Key) Digest() string {
	sum := sha256.Sum256([]byte(k.CodexBinarySHA256 + "\n" + k.ModelPolicySHA256 + "\n" + k.DispatcherSHA256))
	return hex.EncodeToString(sum[:])
}
func (k Key) Valid() bool {
	for _, v := range []string{k.CodexBinarySHA256, k.ModelPolicySHA256, k.DispatcherSHA256} {
		if len(v) != 64 {
			return false
		}
		for _, c := range v {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return true
}

type Cache struct{ Root string }

func NewCache(root string) (*Cache, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("cache root must be absolute")
	}
	return &Cache{Root: filepath.Clean(root)}, nil
}
func (c *Cache) Path(key Key) (string, error) {
	if c == nil || !key.Valid() {
		return "", errors.New("invalid compatibility cache key")
	}
	return filepath.Join(c.Root, key.Digest(), "compatibility.txt"), nil
}
func (c *Cache) Load(key Key) ([]byte, string, error) {
	path, err := c.Path(key)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, "", errors.New("compatibility cache descriptor is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	if !validDescriptor(data) {
		return nil, "", errors.New("malformed compatibility descriptor")
	}
	return data, path, nil
}
func (c *Cache) Store(ctx context.Context, key Key, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := c.Path(key); err != nil {
		return "", err
	}
	if !validDescriptor(data) {
		return "", errors.New("malformed compatibility descriptor")
	}
	if err := os.MkdirAll(c.Root, 0o700); err != nil {
		return "", err
	}
	targetDir := filepath.Join(c.Root, key.Digest())
	if _, err := os.Lstat(targetDir); err == nil {
		_, p, e := c.Load(key)
		return p, e
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	tmp, err := os.MkdirTemp(c.Root, ".compat-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o700); err != nil {
		return "", err
	}
	if err := writeFile(filepath.Join(tmp, "compatibility.txt"), data, 0o600); err != nil {
		return "", err
	}
	if err := writeFile(filepath.Join(tmp, layout.OwnerMarkerName), []byte(layout.OwnerMarkerText), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, targetDir); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	_, p, err := c.Load(key)
	return p, err
}
func writeFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	name := f.Name()
	written := false
	defer func() {
		_ = f.Close()
		if !written {
			_ = os.Remove(name)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	written = true
	return nil
}
func validDescriptor(data []byte) bool {
	if len(data) == 0 || len(data) > 1<<20 {
		return false
	}
	first := strings.SplitN(string(data), "\n", 2)[0]
	return first == "zephyr-codex-compat-v3"
}

const doctorPolicy = "zephyr-codex-model-policy-v1\nprobe\t-\tgpt-5.6-luna\tlow\ttrue\n"

func WriteDoctorPolicy(operationDir string) (string, error) {
	if !filepath.IsAbs(operationDir) {
		return "", errors.New("operation directory must be absolute")
	}
	info, err := os.Lstat(operationDir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("operation directory must be a regular directory")
	}
	path := filepath.Join(operationDir, "doctor-model-policy.tsv")
	if _, err := os.Lstat(path); err == nil {
		return "", errors.New("doctor policy already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	tmp, err := os.CreateTemp(operationDir, ".doctor-policy-")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.WriteString(doctorPolicy)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("write doctor policy: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		return "", err
	}
	return path, nil
}
