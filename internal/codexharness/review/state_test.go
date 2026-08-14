package review

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateAdvancesInOrderAndInspectsAfterTerminalFailure(t *testing.T) {
	state := NewState("run-1", nil)
	require.NoError(t, state.Complete(StageHostPreflight))
	require.NoError(t, state.Complete(StageCoreInit))
	require.NoError(t, state.Fail(StageCollection))
	assert.Equal(t, StatusFailed, state.Status())
	require.NoError(t, state.FinalizeInspect())
	assert.Error(t, state.FinalizeInspect())
}

func TestStateRejectsSkippedAndDuplicateStage(t *testing.T) {
	state := NewState("run-1", nil)
	assert.Error(t, state.Complete(StageCollection))
	require.NoError(t, state.Complete(StageHostPreflight))
	assert.Error(t, state.Complete(StageHostPreflight))
}

func TestStateAllowsNotApplicableAndDegradedTerminal(t *testing.T) {
	state := NewState("run-1", nil)
	for _, stage := range []Stage{StageHostPreflight, StageCoreInit, StageCollection, StageCapabilities, StageCompatibility, StageRoute} {
		require.NoError(t, state.Complete(stage))
	}
	require.NoError(t, state.NotApplicable(StageSemantic))
	require.NoError(t, state.MarkDegraded())
	require.NoError(t, state.Complete(StageReview))
	require.NoError(t, state.Complete(StageEvidenceInput))
	require.NoError(t, state.Complete(StageEvidence))
	require.NoError(t, state.Complete(StageAggregate))
	require.NoError(t, state.Complete(StageRender))
	assert.Equal(t, StatusCompleteWithLimits, state.Status())
}

func TestStateRecordsIncompleteStage(t *testing.T) {
	state := NewState("", nil)
	require.NoError(t, state.Complete(StageHostPreflight))
	state.SetRunID("run-1")
	require.NoError(t, state.Complete(StageCoreInit))
	require.NoError(t, state.Incomplete(StageCollection))

	assert.Equal(t, StatusIncomplete, state.Status())
	assert.Equal(t, StageCollection, state.FailedStage())
	require.NoError(t, state.FinalizeInspect())
}
