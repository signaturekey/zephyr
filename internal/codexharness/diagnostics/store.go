package diagnostics

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/signaturekey/zephyr/internal/codexharness/layout"
)

type StoreOption func(*Store)

func WithPrivateDiagnostics() StoreOption {
	return func(store *Store) { store.private = true }
}

func WithRetention(policy layout.RetentionPolicy) StoreOption {
	return func(store *Store) { store.retention = policy }
}

type Store struct {
	roots     layout.Roots
	private   bool
	retention layout.RetentionPolicy
}

type Operation struct {
	ID              string
	Root            string
	DiagnosticsPath string
	OutputsDir      string
	PrivateDir      string
}

func NewStore(roots layout.Roots, options ...StoreOption) (*Store, error) {
	if err := layout.ValidateManagedRoots(roots); err != nil {
		return nil, err
	}
	store := &Store{roots: roots, retention: layout.DefaultRetentionPolicy()}
	for _, option := range options {
		option(store)
	}
	return store, nil
}

func (store *Store) Begin() (Operation, error) {
	if err := layout.ValidateManagedRoots(store.roots); err != nil {
		return Operation{}, fmt.Errorf("validate operation roots: %w", err)
	}
	if err := secureMkdirAll(store.roots.DriverRoot); err != nil {
		return Operation{}, fmt.Errorf("create driver root: %w", err)
	}
	if err := secureMkdirAll(store.roots.Operation); err != nil {
		return Operation{}, fmt.Errorf("create operations root: %w", err)
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Operation{}, fmt.Errorf("generate operation ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	root := filepath.Join(store.roots.Operation, id)
	if err := os.Mkdir(root, 0o700); err != nil {
		return Operation{}, fmt.Errorf("create operation directory: %w", err)
	}
	operation := Operation{
		ID:              id,
		Root:            root,
		DiagnosticsPath: filepath.Join(root, "diagnostics.json"),
		OutputsDir:      filepath.Join(root, "outputs"),
	}
	if err := os.WriteFile(filepath.Join(root, layout.OwnerMarkerName), []byte(layout.OwnerMarkerText), 0o600); err != nil {
		return Operation{}, fmt.Errorf("write operation ownership marker: %w", err)
	}
	if err := os.Mkdir(operation.OutputsDir, 0o700); err != nil {
		return Operation{}, fmt.Errorf("create operation outputs: %w", err)
	}
	if store.private {
		operation.PrivateDir = filepath.Join(root, "private")
		if err := os.Mkdir(operation.PrivateDir, 0o700); err != nil {
			return Operation{}, fmt.Errorf("create private diagnostics: %w", err)
		}
	}
	return operation, nil
}

func (store *Store) Finalize(ctx context.Context, operation Operation, document Document) ([]layout.CoverageEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("finalize diagnostics: %w", err)
	}
	if err := layout.ValidateManagedRoots(store.roots); err != nil {
		return nil, fmt.Errorf("validate finalization roots: %w", err)
	}
	if err := store.validateOperation(operation); err != nil {
		return nil, err
	}
	outputsInfo, err := os.Lstat(operation.OutputsDir)
	if err != nil {
		return nil, fmt.Errorf("inspect operation outputs: %w", err)
	}
	if !outputsInfo.IsDir() || outputsInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("operation outputs must be a non-symlink directory")
	}
	if operation.PrivateDir != "" {
		privateInfo, err := os.Lstat(operation.PrivateDir)
		if err != nil || !privateInfo.IsDir() || privateInfo.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("private diagnostics must be a non-symlink directory")
		}
		if !containsWarning(document.Warnings, WarningPrivateRetained) {
			document.Warnings = append(document.Warnings, WarningPrivateRetained)
		}
	}
	data, err := Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("validate diagnostics: %w", err)
	}
	if err := atomicWrite(ctx, operation.DiagnosticsPath, data); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(operation.OutputsDir); err != nil {
		return nil, fmt.Errorf("remove finalized raw outputs: %w", err)
	}
	events, err := layout.Prune(store.roots.DriverRoot, store.retention, timeNow())
	if err != nil {
		return events, fmt.Errorf("prune diagnostics retention: %w", err)
	}
	return events, nil
}

var timeNow = func() time.Time { return time.Now().UTC() }

func (store *Store) validateOperation(operation Operation) error {
	if len(operation.ID) != 32 {
		return errors.New("invalid operation ID")
	}
	if _, err := hex.DecodeString(operation.ID); err != nil {
		return errors.New("invalid operation ID")
	}
	expectedRoot := filepath.Join(store.roots.Operation, operation.ID)
	if operation.Root != expectedRoot || operation.DiagnosticsPath != filepath.Join(expectedRoot, "diagnostics.json") || operation.OutputsDir != filepath.Join(expectedRoot, "outputs") {
		return errors.New("operation paths do not belong to this store")
	}
	if operation.PrivateDir != "" && operation.PrivateDir != filepath.Join(expectedRoot, "private") {
		return errors.New("private path does not belong to this operation")
	}
	return nil
}

func secureMkdirAll(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func atomicWrite(ctx context.Context, path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".diagnostics-*")
	if err != nil {
		return fmt.Errorf("create diagnostics temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure diagnostics temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write diagnostics temporary file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync diagnostics temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close diagnostics temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish diagnostics: %w", err)
	}
	return nil
}

func containsWarning(warnings []Warning, expected Warning) bool {
	for _, warning := range warnings {
		if warning == expected {
			return true
		}
	}
	return false
}
