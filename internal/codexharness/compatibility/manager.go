package compatibility

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	zephyrassets "github.com/signaturekey/zephyr"
	"github.com/signaturekey/zephyr/internal/codexharness/dispatch"
	"github.com/signaturekey/zephyr/internal/codexharness/trust"
)

type RuntimeChecker interface {
	Probe(context.Context, dispatch.ProbeRequest) (dispatch.Result, error)
	Smoke(context.Context, dispatch.Common) (dispatch.Result, error)
}
type Result struct {
	DescriptorPath string
	CacheHit       bool
	LiveSmokeDone  bool
}
type Manager struct {
	Cache          *Cache
	Checker        RuntimeChecker
	CodexPath      string
	DispatcherPath string
	Assets         fs.FS
}

func (m *Manager) Ensure(ctx context.Context, policyPath, operationDir, privateDir string) (Result, error) {
	if m.Cache == nil || m.Checker == nil {
		return Result{}, errors.New("compatibility manager is not configured")
	}
	if err := regular(policyPath); err != nil {
		return Result{}, fmt.Errorf("model policy: %w", err)
	}
	if err := directory(operationDir); err != nil {
		return Result{}, fmt.Errorf("compatibility operation directory: %w", err)
	}
	assets := m.Assets
	if assets == nil {
		assets = zephyrassets.Harness
	}
	verified, err := trust.VerifyDispatcher(m.DispatcherPath, assets)
	if err != nil {
		return Result{}, fmt.Errorf("dispatcher-integrity-failed: %w", err)
	}
	codexHash, err := trust.HashRegular(m.CodexPath, true)
	if err != nil {
		return Result{}, err
	}
	policyHash, err := trust.HashRegular(policyPath, false)
	if err != nil {
		return Result{}, err
	}
	key := Key{CodexBinarySHA256: codexHash, ModelPolicySHA256: policyHash, DispatcherSHA256: verified.SHA256}
	if data, _, err := m.Cache.Load(key); err == nil {
		descriptor, err := copyDescriptor(operationDir, "compatibility-smoke.txt", data)
		if err != nil {
			return Result{}, err
		}
		_, err = m.Checker.Smoke(ctx, dispatch.Common{PolicyPath: policyPath, CompatibilityPath: descriptor, OutputPath: privateOutput(operationDir, "smoke.json"), PrivateDiagnosticsDir: privateDir})
		if err != nil {
			return Result{}, err
		}
		return Result{DescriptorPath: descriptor, CacheHit: true, LiveSmokeDone: true}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	probeOut := privateOutput(operationDir, "compatibility-probe.txt")
	probed, err := m.Checker.Probe(ctx, dispatch.ProbeRequest{PolicyPath: policyPath, OutputPath: probeOut, PrivateDiagnosticsDir: privateDir})
	if err != nil {
		return Result{}, err
	}
	if probed.OutputPath != probeOut {
		return Result{}, errors.New("dispatcher returned unexpected probe output")
	}
	data, err := os.ReadFile(probeOut)
	if err != nil {
		return Result{}, err
	}
	if _, err := m.Cache.Store(ctx, key, data); err != nil {
		return Result{}, err
	}
	descriptor, err := copyDescriptor(operationDir, "compatibility.txt", data)
	if err != nil {
		return Result{}, err
	}
	return Result{DescriptorPath: descriptor}, nil
}
func privateOutput(dir, name string) string { return filepath.Join(dir, name) }
func regular(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("path must be absolute")
	}
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() || i.Mode()&os.ModeSymlink != 0 {
		return errors.New("must be a regular non-symlink file")
	}
	return nil
}
func directory(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("path must be absolute")
	}
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return errors.New("must be a non-symlink directory")
	}
	return nil
}
func copyDescriptor(dir, name string, data []byte) (string, error) {
	path := filepath.Join(dir, name)
	if _, e := os.Lstat(path); e == nil {
		return "", errors.New("compatibility output already exists")
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if e != nil {
		return "", e
	}
	defer f.Close()
	if _, e = io.Copy(f, bytesReader(data)); e != nil {
		return "", e
	}
	if e = f.Sync(); e != nil {
		return "", e
	}
	return path, f.Close()
}

type byteReader []byte

func bytesReader(v []byte) *byteReader { r := byteReader(v); return &r }
func (r *byteReader) Read(p []byte) (int, error) {
	if len(*r) == 0 {
		return 0, io.EOF
	}
	n := copy(p, *r)
	*r = (*r)[n:]
	return n, nil
}
