package workflow

import (
	"errors"
	"testing"

	"github.com/signaturekey/zephyr/internal/trace"
	"github.com/stretchr/testify/assert"
)

type failingTraceFinisher struct{}

func (failingTraceFinisher) finish(trace.Status, error) error {
	return errors.New("injected trace persistence failure")
}

func TestCommittedRoutingRemainsSuccessfulWhenTraceFinalizationFails(t *testing.T) {
	committed := FinalizeRoutingResult{RunID: "run-1", RoutingPath: "/private/routing.json"}
	result := finishCommittedRoutingTrace(committed, failingTraceFinisher{}, trace.StatusCompleted, nil)
	assert.Equal(t, committed.RunID, result.RunID, "committed routing run ID was lost")
	assert.Equal(t, committed.RoutingPath, result.RoutingPath, "committed routing path was lost")
	assert.NotEmpty(t, result.TraceWarning, "post-commit trace failure was not exposed as a warning")
}
