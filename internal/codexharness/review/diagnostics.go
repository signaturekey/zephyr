package review

import (
	"context"

	"github.com/signaturekey/zephyr/internal/codexharness/diagnostics"
)

func FinalizeDiagnostics(ctx context.Context, prepared Prepared, result Result) error {
	options := []diagnostics.StoreOption{}
	if prepared.Operation.PrivateDir != "" {
		options = append(options, diagnostics.WithPrivateDiagnostics())
	}
	store, err := diagnostics.NewStore(prepared.Roots, options...)
	if err != nil {
		return err
	}
	terminal := diagnostics.TerminalFailed
	switch Status(result.Status) {
	case StatusComplete, StatusCompleteWithLimits:
		terminal = diagnostics.TerminalComplete
	case StatusIncomplete, StatusStale:
		terminal = diagnostics.TerminalIncomplete
	}
	_, err = store.Finalize(ctx, diagnostics.Operation{
		ID:              prepared.Operation.ID,
		Root:            prepared.Operation.Root,
		DiagnosticsPath: prepared.Operation.DiagnosticsPath,
		OutputsDir:      prepared.Operation.OutputsDir,
		PrivateDir:      prepared.Operation.PrivateDir,
	}, diagnostics.Document{
		Version:       diagnostics.Version,
		OperationID:   prepared.Operation.ID,
		RunID:         result.RunID,
		TerminalState: terminal,
		Coverage: diagnostics.CoverageCounts{
			Selected: len(result.FailedRoles),
			Failed:   len(result.FailedRoles),
		},
		Events: []diagnostics.Event{},
	})
	return err
}
