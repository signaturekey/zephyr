package review

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/signaturekey/zephyr/internal/codexharness/diagnostics"
	"github.com/signaturekey/zephyr/internal/codexharness/layout"
	"github.com/signaturekey/zephyr/internal/codexharness/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_FinalizesEvidenceAndRenders(t *testing.T) {
	core := newServiceFakeCore()
	core.route = RouteResult{RunID: "run-abcd", PacketPath: "/packet.json", RoutingRequest: json.RawMessage(`{"candidates":[]}`), Routing: json.RawMessage(`{"selected":[{"role":"code-reviewer"}]}`)}
	dispatcher := &serviceFakeDispatcher{}
	service := NewService(Dependencies{Prepare: fakePreparation(t), Preflight: serviceFakePreflight{}, Core: core, Dispatcher: dispatcher, Compatibility: serviceFakeCompatibility{}, Scheduler: inlineScheduler{}})

	result, err := service.Review(context.Background(), ReviewOptions{Repository: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, string(StatusComplete), result.Status)
	assert.Equal(t, 1, dispatcher.evidenceCalls)
	assert.Equal(t, []string{"prepare-evidence", "validate-verdicts", "aggregate", "render", "inspect"}, core.calls)
}

type recordingLifecycle struct{ events []StateEvent }

func (sink *recordingLifecycle) RecordState(event StateEvent) {
	sink.events = append(sink.events, event)
}

func TestServiceUsesStateMachineForSuccessfulLifecycle(t *testing.T) {
	core := newServiceFakeCore()
	core.route = RouteResult{RunID: "run-abcd", PacketPath: "/packet.json", RoutingRequest: json.RawMessage(`{"candidates":[]}`), Routing: json.RawMessage(`{"selected":[{"role":"code-reviewer"}]}`)}
	dispatcher := &serviceFakeDispatcher{}
	sink := &recordingLifecycle{}
	service := NewService(Dependencies{Prepare: fakePreparation(t), Preflight: serviceFakePreflight{}, Core: core, Dispatcher: dispatcher, Compatibility: serviceFakeCompatibility{}, Scheduler: inlineScheduler{}, Lifecycle: sink})

	result, err := service.Review(context.Background(), ReviewOptions{Repository: t.TempDir()})

	require.NoError(t, err)
	assert.Equal(t, string(StatusComplete), result.Status)
	assert.Equal(t, []StateEvent{
		{Stage: StageHostPreflight, Outcome: OutcomeComplete},
		{Stage: StageCoreInit, Outcome: OutcomeComplete},
		{Stage: StageCollection, Outcome: OutcomeComplete},
		{Stage: StageCapabilities, Outcome: OutcomeComplete},
		{Stage: StageCompatibility, Outcome: OutcomeComplete},
		{Stage: StageRoute, Outcome: OutcomeComplete},
		{Stage: StageSemantic, Outcome: OutcomeNotApplicable},
		{Stage: StageReview, Outcome: OutcomeComplete},
		{Stage: StageEvidenceInput, Outcome: OutcomeComplete},
		{Stage: StageEvidence, Outcome: OutcomeComplete},
		{Stage: StageAggregate, Outcome: OutcomeComplete},
		{Stage: StageRender, Outcome: OutcomeComplete},
		{Stage: StageInspect, Outcome: OutcomeComplete},
	}, sink.events)
}

func TestService_EvidenceFailureMarksRunIncompleteWithoutRender(t *testing.T) {
	core := newServiceFakeCore()
	core.route = RouteResult{RunID: "run-abcd", PacketPath: "/packet.json", RoutingRequest: json.RawMessage(`{"candidates":[]}`), Routing: json.RawMessage(`{"selected":[{"role":"code-reviewer"}]}`)}
	dispatcher := &serviceFakeDispatcher{evidenceErr: errors.New("evidence down")}
	service := NewService(Dependencies{Prepare: fakePreparation(t), Preflight: serviceFakePreflight{}, Core: core, Dispatcher: dispatcher, Compatibility: serviceFakeCompatibility{}, Scheduler: inlineScheduler{}})

	result, err := service.Review(context.Background(), ReviewOptions{Repository: t.TempDir()})
	require.ErrorIs(t, err, ErrIncompleteReview)
	assert.Equal(t, string(StatusIncomplete), result.Status)
	assert.Equal(t, 1, dispatcher.evidenceCalls)
	assert.Equal(t, []string{"prepare-evidence", "mark-evidence-failed", "inspect"}, core.calls)
}

var _ scheduler.Job

func TestFinalizeDiagnosticsPublishesSafeDocumentAndRemovesRawOutputs(t *testing.T) {
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	require.NoError(t, os.Mkdir(repository, 0o700))
	roots, err := layout.Resolve(repository, filepath.Join(base, "driver"))
	require.NoError(t, err)
	store, err := diagnostics.NewStore(roots)
	require.NoError(t, err)
	operation, err := store.Begin()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(operation.OutputsDir, "raw.json"), []byte("private model output"), 0o600))
	prepared := Prepared{Repository: repository, Roots: roots, Operation: Operation{
		ID: operation.ID, Root: operation.Root, DiagnosticsPath: operation.DiagnosticsPath, OutputsDir: operation.OutputsDir,
	}}
	result := Result{OperationID: operation.ID, RunID: "run-0123", Status: string(StatusComplete), DiagnosticsPath: operation.DiagnosticsPath, FailedRoles: []string{}}

	err = FinalizeDiagnostics(context.Background(), prepared, result)

	require.NoError(t, err)
	assert.FileExists(t, operation.DiagnosticsPath)
	assert.NoDirExists(t, operation.OutputsDir)
	data, err := os.ReadFile(operation.DiagnosticsPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "private model output")
}
