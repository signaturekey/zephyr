package diagnostics_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signaturekey/zephyr/internal/codexharness/diagnostics"
	"github.com/signaturekey/zephyr/internal/codexharness/layout"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "sk-test-secret"

func TestMarshalContainsOnlySafeDiagnosticsAndStderrFingerprint(t *testing.T) {
	raw := strings.Join([]string{
		testSecret,
		`{"tokens":{"access_token":"auth-json-secret"}}`,
		"review this private prompt",
		"diff --git a/secret.go b/secret.go",
		"raw stderr details",
		`{"model_response":"private finding"}`,
	}, "\n")
	event := diagnostics.NewEvent("review", "golang-expert", diagnostics.CategoryAuth, "codex-auth-failed", 1, false, 1, 4, []byte(raw))
	document := validDocument(event)

	encoded, err := diagnostics.Marshal(document)

	require.NoError(t, err)
	for _, forbidden := range []string{testSecret, "auth-json-secret", "private prompt", "diff --git", "raw stderr details", "private finding"} {
		assert.NotContains(t, string(encoded), forbidden)
	}
	assert.Contains(t, string(encoded), `"stderr_bytes":`)
	assert.Contains(t, string(encoded), `"stderr_sha256":`)
}

func TestMarshalRejectsUnsafeFreeTextAndUnknownEnums(t *testing.T) {
	document := validDocument(diagnostics.NewEvent("review", "", diagnostics.CategoryTimeout, "deadline", -1, true, 1, 1, nil))
	document.CoreVersion = "v1\n" + testSecret
	encoded, err := diagnostics.Marshal(document)
	require.Error(t, err)
	assert.NotContains(t, string(encoded), testSecret)

	document = validDocument(diagnostics.Event{Stage: "review", Category: diagnostics.Category("made-up"), ReasonCode: "bad"})
	_, err = diagnostics.Marshal(document)
	require.Error(t, err)
}

func TestMarshalRejectsSecretInEveryCallerControlledIdentityField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*diagnostics.Document)
	}{
		{name: "operation ID", mutate: func(document *diagnostics.Document) { document.OperationID = testSecret }},
		{name: "run ID", mutate: func(document *diagnostics.Document) { document.RunID = testSecret }},
		{name: "core version", mutate: func(document *diagnostics.Document) { document.CoreVersion = testSecret }},
		{name: "codex version", mutate: func(document *diagnostics.Document) { document.CodexVersion = testSecret }},
		{name: "event stage", mutate: func(document *diagnostics.Document) { document.Events[0].Stage = testSecret }},
		{name: "event role", mutate: func(document *diagnostics.Document) { document.Events[0].Role = testSecret }},
		{name: "event reason", mutate: func(document *diagnostics.Document) { document.Events[0].ReasonCode = testSecret }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := validDocument(diagnostics.NewEvent("review", "golang-expert", diagnostics.CategoryAuth, "codex-auth-failed", 1, false, 1, 1, nil))
			test.mutate(&document)

			encoded, err := diagnostics.Marshal(document)

			require.Error(t, err)
			assert.NotContains(t, string(encoded), testSecret)
			assert.NotContains(t, err.Error(), testSecret)
		})
	}
}

func TestStoreCreatesPrivateOperationWithRestrictedModesAndRandomID(t *testing.T) {
	roots := resolveRoots(t)
	store, err := diagnostics.NewStore(roots, diagnostics.WithPrivateDiagnostics())
	require.NoError(t, err)

	first, err := store.Begin()
	require.NoError(t, err)
	second, err := store.Begin()
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID)
	assert.Regexp(t, `^[0-9a-f]{32}$`, first.ID)
	for _, directory := range []string{first.Root, first.OutputsDir, first.PrivateDir} {
		assertMode(t, directory, 0o700)
	}
	assertMode(t, filepath.Join(first.Root, layout.OwnerMarkerName), 0o600)
	marker, err := os.ReadFile(filepath.Join(first.Root, layout.OwnerMarkerName))
	require.NoError(t, err)
	assert.Equal(t, layout.OwnerMarkerText, string(marker))
}

func TestFinalizeAtomicallyPublishesSafeJSONDeletesOutputsAndKeepsPrivate(t *testing.T) {
	roots := resolveRoots(t)
	store, err := diagnostics.NewStore(roots, diagnostics.WithPrivateDiagnostics())
	require.NoError(t, err)
	operation, err := store.Begin()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(operation.OutputsDir, "raw-response.json"), []byte(testSecret), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(operation.PrivateDir, "explicit.txt"), []byte(testSecret), 0o600))
	document := validDocument(diagnostics.NewEvent("review", "", diagnostics.CategoryTransport, "connection-reset", 1, false, 1, 2, []byte(testSecret)))
	document.OperationID = operation.ID

	_, err = store.Finalize(context.Background(), operation, document)
	require.NoError(t, err)

	assert.NoDirExists(t, operation.OutputsDir)
	assert.DirExists(t, operation.PrivateDir)
	assertMode(t, operation.DiagnosticsPath, 0o600)
	data, err := os.ReadFile(operation.DiagnosticsPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), testSecret)
	assert.Contains(t, string(data), string(diagnostics.WarningPrivateRetained))
	entries, err := os.ReadDir(operation.Root)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.False(t, strings.HasPrefix(entry.Name(), ".diagnostics-"), "atomic temp file was left behind")
	}
}

func TestFinalizePrunesOnlyAfterCurrentDiagnosticsExists(t *testing.T) {
	roots := resolveRoots(t)
	store, err := diagnostics.NewStore(roots, diagnostics.WithRetention(layout.RetentionPolicy{
		OperationMaxAge: 365 * 24 * time.Hour,
		OperationMax:    1,
		CacheMaxAge:     365 * 24 * time.Hour,
		CacheMax:        8,
	}))
	require.NoError(t, err)
	old, err := store.Begin()
	require.NoError(t, err)
	oldDocument := validDocument()
	oldDocument.OperationID = old.ID
	_, err = store.Finalize(context.Background(), old, oldDocument)
	require.NoError(t, err)
	oldTime := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(old.Root, oldTime, oldTime))
	current, err := store.Begin()
	require.NoError(t, err)
	currentDocument := validDocument()
	currentDocument.OperationID = current.ID

	_, err = store.Finalize(context.Background(), current, currentDocument)
	require.NoError(t, err)

	assert.NoDirExists(t, old.Root)
	assert.FileExists(t, current.DiagnosticsPath)
}

func TestFinalizeReturnsSafeRetentionCoverageEvents(t *testing.T) {
	roots := resolveRoots(t)
	store, err := diagnostics.NewStore(roots)
	require.NoError(t, err)
	operation, err := store.Begin()
	require.NoError(t, err)
	foreignName := "foreign-sk-test-secret"
	require.NoError(t, os.MkdirAll(filepath.Join(roots.CacheRoot, foreignName), 0o700))
	document := validDocument()
	document.OperationID = operation.ID

	events, err := store.Finalize(context.Background(), operation, document)

	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, layout.CoverageCache, events[0].Collection)
	assert.Equal(t, layout.CoverageForeignEntry, events[0].Reason)
	encoded, err := json.Marshal(events)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), foreignName)
}

func TestStoreRejectsSymlinkComponentsAndDoesNotDeleteSymlinkOutputTarget(t *testing.T) {
	base := t.TempDir()
	realDriver := filepath.Join(base, "driver")
	require.NoError(t, os.Mkdir(realDriver, 0o700))
	linkedDriver := filepath.Join(base, "linked-driver")
	require.NoError(t, os.Symlink(realDriver, linkedDriver))
	_, err := diagnostics.NewStore(layout.Roots{
		DriverRoot: linkedDriver,
		Operation:  filepath.Join(linkedDriver, "operations"),
		RunRoot:    filepath.Join(linkedDriver, "runs"),
		CacheRoot:  filepath.Join(linkedDriver, "cache"),
	})
	require.Error(t, err)

	roots := resolveRoots(t)
	store, err := diagnostics.NewStore(roots)
	require.NoError(t, err)
	operation, err := store.Begin()
	require.NoError(t, err)
	require.NoError(t, os.Remove(operation.OutputsDir))
	external := t.TempDir()
	protected := filepath.Join(external, "protected.txt")
	require.NoError(t, os.WriteFile(protected, []byte(testSecret), 0o600))
	require.NoError(t, os.Symlink(external, operation.OutputsDir))
	document := validDocument()
	document.OperationID = operation.ID

	_, err = store.Finalize(context.Background(), operation, document)

	require.Error(t, err)
	assert.FileExists(t, protected)
}

func TestNewStoreRejectsSymlinkedManagedRoot(t *testing.T) {
	for _, field := range []string{"operations", "runs", "cache"} {
		t.Run(field, func(t *testing.T) {
			roots := resolveRoots(t)
			require.NoError(t, os.MkdirAll(roots.DriverRoot, 0o700))
			target := t.TempDir()
			managed := map[string]string{
				"operations": roots.Operation,
				"runs":       roots.RunRoot,
				"cache":      roots.CacheRoot,
			}[field]
			require.NoError(t, os.Symlink(target, managed))

			_, err := diagnostics.NewStore(roots)

			require.Error(t, err)
			assert.Empty(t, directoryNames(t, target))
		})
	}
}

func TestStoreRevalidatesManagedRootsBeforeBeginAndFinalize(t *testing.T) {
	t.Run("begin", func(t *testing.T) {
		roots := resolveRoots(t)
		store, err := diagnostics.NewStore(roots)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(roots.DriverRoot, 0o700))
		target := t.TempDir()
		require.NoError(t, os.Symlink(target, roots.Operation))

		_, err = store.Begin()

		require.Error(t, err)
		assert.Empty(t, directoryNames(t, target))
	})

	t.Run("finalize", func(t *testing.T) {
		roots := resolveRoots(t)
		store, err := diagnostics.NewStore(roots)
		require.NoError(t, err)
		operation, err := store.Begin()
		require.NoError(t, err)
		target := t.TempDir()
		require.NoError(t, os.Symlink(target, roots.CacheRoot))
		document := validDocument()
		document.OperationID = operation.ID

		_, err = store.Finalize(context.Background(), operation, document)

		require.Error(t, err)
		assert.NoFileExists(t, operation.DiagnosticsPath)
		assert.Empty(t, directoryNames(t, target))
	})
}

func validDocument(events ...diagnostics.Event) diagnostics.Document {
	return diagnostics.Document{
		Version:          diagnostics.Version,
		OperationID:      "0123456789abcdef0123456789abcdef",
		RunID:            "run-0123",
		CoreVersion:      "v1.2.3",
		CodexVersion:     "codex-1.0.0",
		CoreSHA256:       strings.Repeat("a", 64),
		CodexSHA256:      strings.Repeat("b", 64),
		PolicySHA256:     strings.Repeat("c", 64),
		DispatcherSHA256: strings.Repeat("d", 64),
		TerminalState:    diagnostics.TerminalComplete,
		Coverage: diagnostics.CoverageCounts{
			Selected:  3,
			Completed: 2,
			Failed:    1,
		},
		Events: events,
	}
}

func resolveRoots(t *testing.T) layout.Roots {
	t.Helper()
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	require.NoError(t, os.Mkdir(repository, 0o700))
	roots, err := layout.Resolve(repository, filepath.Join(base, "driver"))
	require.NoError(t, err)
	return roots
}

func assertMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, expected, info.Mode().Perm())
}

func directoryNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	require.NoError(t, err)
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.Name())
	}
	return result
}

func TestFinalizedDocumentIsValidJSON(t *testing.T) {
	encoded, err := diagnostics.Marshal(validDocument())
	require.NoError(t, err)
	assert.True(t, json.Valid(encoded))
}
