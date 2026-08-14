package review

import (
	"errors"
	"fmt"
)

type Stage string

const (
	StageHostPreflight Stage = "host-preflight"
	StageCoreInit      Stage = "core-init"
	StageCollection    Stage = "collection"
	StageCapabilities  Stage = "capabilities"
	StageCompatibility Stage = "compatibility"
	StageRoute         Stage = "route"
	StageSemantic      Stage = "semantic-routing"
	StageReview        Stage = "review"
	StageEvidenceInput Stage = "evidence-input"
	StageEvidence      Stage = "evidence"
	StageAggregate     Stage = "aggregate"
	StageRender        Stage = "render"
	StageInspect       Stage = "inspect"
)

var stages = []Stage{StageHostPreflight, StageCoreInit, StageCollection, StageCapabilities, StageCompatibility, StageRoute, StageSemantic, StageReview, StageEvidenceInput, StageEvidence, StageAggregate, StageRender}

type Outcome string

const (
	OutcomeComplete      Outcome = "complete"
	OutcomeFailed        Outcome = "failed"
	OutcomeIncomplete    Outcome = "incomplete"
	OutcomeNotApplicable Outcome = "not-applicable"
)

type Status string

const (
	StatusComplete           Status = "complete"
	StatusCompleteWithLimits Status = "complete-with-limits"
	StatusIncomplete         Status = "incomplete"
	StatusStale              Status = "stale"
	StatusFailed             Status = "failed"
)

type StateEvent struct {
	Stage   Stage
	Outcome Outcome
}
type Diagnostics interface{ RecordState(StateEvent) }

type State struct {
	runID     string
	next      int
	events    []StateEvent
	sink      Diagnostics
	terminal  Status
	inspected bool
	limits    bool
	failed    Stage
}

func NewState(runID string, sink Diagnostics) *State   { return &State{runID: runID, sink: sink} }
func NewMachine(runID string, sink Diagnostics) *State { return NewState(runID, sink) }
func (state *State) RunID() string                     { return state.runID }
func (state *State) SetRunID(runID string)             { state.runID = runID }
func (state *State) Status() Status                    { return state.terminal }
func (state *State) FailedStage() Stage                { return state.failed }
func (state *State) Events() []StateEvent              { return append([]StateEvent(nil), state.events...) }
func (state *State) Complete(stage Stage) error        { return state.transition(stage, OutcomeComplete) }
func (state *State) Fail(stage Stage) error            { return state.transition(stage, OutcomeFailed) }
func (state *State) Incomplete(stage Stage) error      { return state.transition(stage, OutcomeIncomplete) }
func (state *State) NotApplicable(stage Stage) error {
	return state.transition(stage, OutcomeNotApplicable)
}
func (state *State) Transition(stage Stage, outcome Outcome) error {
	return state.transition(stage, outcome)
}
func (state *State) MarkDegraded() error {
	if state.terminal != "" {
		return errors.New("driver is terminal")
	}
	state.limits = true
	return nil
}
func (state *State) MarkIncomplete() error {
	if state.terminal != "" {
		return errors.New("driver is terminal")
	}
	state.terminal = StatusIncomplete
	return nil
}
func (state *State) MarkStale() error {
	if state.terminal != "" {
		return errors.New("driver is terminal")
	}
	state.terminal = StatusStale
	return nil
}
func (state *State) Interrupt() error { return state.MarkIncomplete() }
func (state *State) FinalizeInspect() error {
	if state.runID == "" {
		return errors.New("cannot inspect without a run ID")
	}
	if state.inspected {
		return errors.New("inspect already finalized")
	}
	state.inspected = true
	state.record(StateEvent{Stage: StageInspect, Outcome: OutcomeComplete})
	return nil
}
func (state *State) transition(stage Stage, outcome Outcome) error {
	if state.terminal != "" {
		return fmt.Errorf("driver is terminal: %s", state.terminal)
	}
	if state.next >= len(stages) || stages[state.next] != stage {
		return fmt.Errorf("illegal stage transition to %q", stage)
	}
	if outcome != OutcomeComplete && outcome != OutcomeFailed && outcome != OutcomeIncomplete && outcome != OutcomeNotApplicable {
		return fmt.Errorf("unknown outcome %q", outcome)
	}
	state.next++
	state.record(StateEvent{Stage: stage, Outcome: outcome})
	if outcome == OutcomeFailed {
		state.failed = stage
		state.terminal = StatusFailed
		return nil
	}
	if outcome == OutcomeIncomplete {
		state.failed = stage
		state.terminal = StatusIncomplete
		return nil
	}
	if state.next == len(stages) {
		if state.limits {
			state.terminal = StatusCompleteWithLimits
		} else {
			state.terminal = StatusComplete
		}
	}
	return nil
}
func (state *State) record(event StateEvent) {
	state.events = append(state.events, event)
	if state.sink != nil {
		state.sink.RecordState(event)
	}
}
