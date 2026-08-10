package workflow

import (
	"errors"
	"testing"

	"github.com/signaturekey/zephyr/internal/trace"
)

type failingTraceFinisher struct{}

func (failingTraceFinisher) finish(trace.Status, error) error {
	return errors.New("injected trace persistence failure")
}

func TestCommittedRoutingRemainsSuccessfulWhenTraceFinalizationFails(t *testing.T) {
	committed := FinalizeRoutingResult{RunID: "run-1", RoutingPath: "/private/routing.json"}
	result := finishCommittedRoutingTrace(committed, failingTraceFinisher{}, trace.StatusCompleted, nil)
	if result.RunID != committed.RunID || result.RoutingPath != committed.RoutingPath {
		t.Fatalf("committed routing was lost: %#v", result)
	}
	if result.TraceWarning == "" {
		t.Fatal("post-commit trace failure was not exposed as a warning")
	}
}
