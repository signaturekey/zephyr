package run

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const ManifestVersion = 1

type Mode string

const (
	ModePlan           Mode = "plan"
	ModeImplementation Mode = "implementation"
	ModeAlignment      Mode = "alignment"
	ModeAuto           Mode = "auto"
)

func (m Mode) Validate() error {
	switch m {
	case ModePlan, ModeImplementation, ModeAlignment, ModeAuto:
		return nil
	default:
		return fmt.Errorf("unknown review mode %q", m)
	}
}

func ResolveMode(mode Mode, hasPlan, hasChanges bool) (Mode, error) {
	if err := mode.Validate(); err != nil {
		return "", err
	}
	if mode != ModeAuto {
		return mode, nil
	}

	switch {
	case hasPlan && hasChanges:
		return ModeAlignment, nil
	case hasPlan:
		return ModePlan, nil
	case hasChanges:
		return ModeImplementation, nil
	default:
		return "", errors.New("auto mode requires a plan, Git changes, or both")
	}
}

type Source string

const (
	SourceWorkingTree Source = "working-tree"
	SourceStaged      Source = "staged"
	SourceBranch      Source = "branch"
	SourceCommitRange Source = "commit-range"
	SourcePlanOnly    Source = "plan-only"
)

func (s Source) Validate() error {
	switch s {
	case SourceWorkingTree, SourceStaged, SourceBranch, SourceCommitRange, SourcePlanOnly:
		return nil
	default:
		return fmt.Errorf("unknown review source %q", s)
	}
}

type State string

const (
	StateCreated    State = "created"
	StateRunning    State = "running"
	StateComplete   State = "complete"
	StateIncomplete State = "incomplete"
	StateFailed     State = "failed"
)

func (s State) Validate() error {
	switch s {
	case StateCreated, StateRunning, StateComplete, StateIncomplete, StateFailed:
		return nil
	default:
		return fmt.Errorf("unknown run state %q", s)
	}
}

type StageState string

const (
	StagePending  StageState = "pending"
	StageRunning  StageState = "running"
	StageComplete StageState = "complete"
	StageFailed   StageState = "failed"
	StageSkipped  StageState = "skipped"
)

func (s StageState) Validate() error {
	switch s {
	case StagePending, StageRunning, StageComplete, StageFailed, StageSkipped:
		return nil
	default:
		return fmt.Errorf("unknown stage state %q", s)
	}
}

type Stage struct {
	Name       string     `json:"name"`
	State      StageState `json:"state"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type GitSnapshotRef struct {
	HeadSHA                string    `json:"head_sha,omitempty"`
	BaseSHA                string    `json:"base_sha,omitempty"`
	TargetSHA              string    `json:"target_sha,omitempty"`
	MergeBaseSHA           string    `json:"merge_base_sha,omitempty"`
	SourceFingerprint      string    `json:"source_fingerprint"`
	WorkingTreeFingerprint string    `json:"working_tree_fingerprint"`
	CollectedAt            time.Time `json:"collected_at"`
}

type Manifest struct {
	Version        int             `json:"version"`
	ID             string          `json:"id"`
	RunDir         string          `json:"run_dir"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Mode           Mode            `json:"mode"`
	Source         Source          `json:"source"`
	Repository     string          `json:"repository"`
	BaseRef        string          `json:"base_ref,omitempty"`
	CommitRange    string          `json:"commit_range,omitempty"`
	PlanPath       string          `json:"plan_path,omitempty"`
	State          State           `json:"state"`
	Stages         []Stage         `json:"stages"`
	GitSnapshot    *GitSnapshotRef `json:"git_snapshot,omitempty"`
	CoverageLimits []string        `json:"coverage_limits,omitempty"`
}

func (m Manifest) Validate() error {
	if m.Version != ManifestVersion {
		return fmt.Errorf("unsupported manifest version %d", m.Version)
	}
	if strings.TrimSpace(m.ID) == "" {
		return errors.New("manifest id is required")
	}
	if strings.TrimSpace(m.RunDir) == "" {
		return errors.New("manifest run_dir is required")
	}
	if m.CreatedAt.IsZero() || m.UpdatedAt.IsZero() {
		return errors.New("manifest timestamps are required")
	}
	if err := m.Mode.Validate(); err != nil {
		return err
	}
	if err := m.Source.Validate(); err != nil {
		return err
	}
	if err := m.State.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(m.Repository) == "" {
		return errors.New("manifest repository is required")
	}
	if m.Source == SourceBranch && strings.TrimSpace(m.BaseRef) == "" {
		return errors.New("branch source requires base_ref")
	}
	if m.Source == SourceCommitRange && strings.TrimSpace(m.CommitRange) == "" {
		return errors.New("commit-range source requires commit_range")
	}
	for i, stage := range m.Stages {
		if strings.TrimSpace(stage.Name) == "" {
			return fmt.Errorf("stage %d has an empty name", i)
		}
		if err := stage.State.Validate(); err != nil {
			return fmt.Errorf("stage %q: %w", stage.Name, err)
		}
	}
	return nil
}

func (m *Manifest) SetStage(name string, state StageState, at time.Time, message string) error {
	if err := state.Validate(); err != nil {
		return err
	}
	for i := range m.Stages {
		if m.Stages[i].Name != name {
			continue
		}
		m.Stages[i].State = state
		m.Stages[i].Error = message
		if state == StageRunning && m.Stages[i].StartedAt == nil {
			value := at.UTC()
			m.Stages[i].StartedAt = &value
		}
		if state == StageComplete || state == StageFailed || state == StageSkipped {
			value := at.UTC()
			m.Stages[i].FinishedAt = &value
		}
		return nil
	}
	return fmt.Errorf("unknown run stage %q", name)
}

func defaultStages(createdAt time.Time) []Stage {
	finished := createdAt.UTC()
	return []Stage{
		{Name: "init", State: StageComplete, StartedAt: &finished, FinishedAt: &finished},
		{Name: "collect", State: StagePending},
		{Name: "route", State: StagePending},
		{Name: "review", State: StagePending},
		{Name: "evidence", State: StagePending},
		{Name: "aggregate", State: StagePending},
		{Name: "render", State: StagePending},
	}
}
