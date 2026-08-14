package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/signaturekey/zephyr/internal/codexharness/budget"
	"github.com/signaturekey/zephyr/internal/codexharness/process"
)

const coreOutputLimit int64 = 8 << 20

type Core interface {
	Version(context.Context) (CoreVersion, error)
	Init(context.Context, string) (InitResult, error)
	Collect(context.Context, string) (CollectResult, error)
	SetCapability(context.Context, string, string) error
	Route(context.Context, string) (RouteResult, error)
	ValidateRouting(context.Context, string, string) (FinalizeRoutingResult, error)
	FallbackRouting(context.Context, string, string) (FinalizeRoutingResult, error)
	ValidateCandidates(context.Context, string, string, string) (ValidateCandidatesResult, error)
	MarkReviewerFailed(context.Context, string, string, string) error
	PrepareEvidence(context.Context, string) (PrepareEvidenceResult, error)
	ValidateVerdicts(context.Context, string, string) (ValidateVerdictsResult, error)
	MarkEvidenceFailed(context.Context, string, string) error
	Aggregate(context.Context, string) (AggregateResult, error)
	Render(context.Context, string) (RenderResult, error)
	Inspect(context.Context, string) (InspectResult, error)
}

type CoreErrorKind string

const (
	CoreErrorValidation CoreErrorKind = "validation"
	CoreErrorOperation  CoreErrorKind = "operation"
	CoreErrorProtocol   CoreErrorKind = "protocol"
	CoreErrorProcess    CoreErrorKind = "process"
)

type CoreError struct {
	Kind     CoreErrorKind
	Op       string
	ExitCode int
}

func (err *CoreError) Error() string {
	if err.ExitCode != 0 {
		return fmt.Sprintf("core %s %s (exit %d)", err.Kind, err.Op, err.ExitCode)
	}
	return fmt.Sprintf("core %s %s", err.Kind, err.Op)
}

type ErrorEnvelope struct {
	Version    int    `json:"version"`
	Operation  string `json:"operation"`
	Kind       string `json:"kind"`
	ReasonCode string `json:"reason_code"`
}
type CoreVersion struct {
	Version                string `json:"version"`
	Commit                 string `json:"commit"`
	Dirty                  string `json:"dirty"`
	ProtocolVersion        int    `json:"protocol_version"`
	CodexHarnessAPIVersion int    `json:"codex_harness_api_version"`
}
type InitResult struct {
	RunID    string `json:"run_id"`
	RunDir   string `json:"run_dir"`
	Manifest string `json:"manifest"`
}
type CollectResult struct {
	RunID             string          `json:"run_id"`
	Mode              string          `json:"mode"`
	Source            string          `json:"source"`
	Reviewable        bool            `json:"reviewable_changes"`
	Stats             json.RawMessage `json:"stats"`
	SnapshotPath      string          `json:"snapshot_path"`
	MetadataPath      string          `json:"metadata_path"`
	StatusPath        string          `json:"status_path"`
	ModelPolicyPath   string          `json:"model_policy_path"`
	ModelPolicySHA256 string          `json:"model_policy_sha256"`
}
type RouteResult struct {
	RunID              string          `json:"run_id"`
	PacketPath         string          `json:"packet_path"`
	RoutingRequestPath string          `json:"routing_request_path"`
	RoutingPath        string          `json:"routing_path"`
	RoutingRequest     json.RawMessage `json:"routing_request"`
	Routing            json.RawMessage `json:"routing"`
}
type FinalizeRoutingResult struct {
	RunID        string          `json:"run_id"`
	RoutingPath  string          `json:"routing_path"`
	Routing      json.RawMessage `json:"routing"`
	TraceWarning string          `json:"trace_warning"`
}
type ValidateCandidatesResult struct {
	RunID            string `json:"run_id"`
	Role             string `json:"role"`
	Accepted         int    `json:"accepted"`
	Rejected         int    `json:"rejected"`
	CandidatePath    string `json:"candidate_path"`
	PrecheckPath     string `json:"precheck_path"`
	CandidateSetPath string `json:"candidate_set_path"`
}
type PrepareEvidenceResult struct {
	RunID        string `json:"run_id"`
	CandidateSet string `json:"candidate_set_path"`
	Evidence     string `json:"evidence_path"`
	Items        int    `json:"items"`
}
type ValidateVerdictsResult struct {
	RunID       string `json:"run_id"`
	Verdicts    int    `json:"verdicts"`
	VerdictPath string `json:"verdict_path"`
}
type AggregateResult struct {
	RunID        string `json:"run_id"`
	Status       string `json:"status"`
	Findings     int    `json:"findings"`
	NeedsHuman   int    `json:"needs_human"`
	Stale        bool   `json:"stale"`
	ReviewPath   string `json:"review_path"`
	RejectedPath string `json:"rejected_path"`
}
type RenderResult struct {
	RunID      string `json:"run_id"`
	Status     string `json:"status"`
	ReviewMD   string `json:"review_md"`
	ReviewJSON string `json:"review_json"`
}
type InspectResult struct {
	RunID          string          `json:"run_id"`
	RunDir         string          `json:"run_dir"`
	State          string          `json:"state"`
	Mode           string          `json:"mode"`
	Source         string          `json:"source"`
	Stages         json.RawMessage `json:"stages"`
	Capabilities   json.RawMessage `json:"capabilities"`
	CoverageLimits []string        `json:"coverage_limits"`
	Staleness      json.RawMessage `json:"staleness,omitempty"`
	Artifacts      struct {
		Manifest        string `json:"manifest"`
		Snapshot        string `json:"snapshot,omitempty"`
		Capabilities    string `json:"capabilities,omitempty"`
		ModelPolicy     string `json:"model_policy,omitempty"`
		Packet          string `json:"packet,omitempty"`
		Routing         string `json:"routing,omitempty"`
		RoutingRequest  string `json:"routing_request,omitempty"`
		Candidates      string `json:"candidates,omitempty"`
		MinimalEvidence string `json:"minimal_evidence,omitempty"`
		Verdicts        string `json:"verdicts,omitempty"`
		ReviewJSON      string `json:"review_json,omitempty"`
		ReviewMarkdown  string `json:"review_markdown,omitempty"`
		Rejected        string `json:"rejected,omitempty"`
		Trace           string `json:"trace,omitempty"`
	} `json:"artifacts"`
	Counts struct {
		SelectedRoles     int            `json:"selected_roles"`
		ValidatedRoles    int            `json:"validated_roles"`
		FailedRoles       int            `json:"failed_roles"`
		ConfirmedFindings int            `json:"confirmed_findings"`
		NeedsHuman        int            `json:"needs_human"`
		BySeverity        map[string]int `json:"by_severity"`
	} `json:"counts"`
	Routing json.RawMessage `json:"routing,omitempty"`
	Review  json.RawMessage `json:"review,omitempty"`
}

type CoreClient struct {
	Path, RunRoot string
	Runner        process.Runner
	CoreTimeout   time.Duration
	Env           []string
}

func NewCoreClient(path, runRoot string, runner process.Runner) *CoreClient {
	return &CoreClient{Path: path, RunRoot: runRoot, Runner: runner, CoreTimeout: budget.CoreCall}
}
func NewCore(path, runRoot string, runner process.Runner) *CoreClient {
	return NewCoreClient(path, runRoot, runner)
}

func (c *CoreClient) Version(x context.Context) (r CoreVersion, e error) {
	e = c.call(x, "version", nil, &r)
	return
}
func (c *CoreClient) Init(x context.Context, repo string) (r InitResult, e error) {
	e = c.call(x, "init", []string{"--repo", repo, "--mode", "implementation", "--source", "working-tree"}, &r)
	return
}
func (c *CoreClient) Collect(x context.Context, id string) (r CollectResult, e error) {
	e = c.call(x, "collect", []string{"--run", id}, &r)
	return
}
func (c *CoreClient) SetCapability(x context.Context, id, source string) error {
	return c.call(x, "context", []string{"capability", "--run", id, "--source", source, "--status", "not-required", "--reason", "local zephyr-codex experiment does not collect business context"}, nil)
}
func (c *CoreClient) Route(x context.Context, id string) (r RouteResult, e error) {
	e = c.call(x, "route", []string{"--run", id}, &r)
	return
}
func (c *CoreClient) ValidateRouting(x context.Context, id, input string) (r FinalizeRoutingResult, e error) {
	e = c.call(x, "validate-routing", []string{"--run", id, "--input", input}, &r)
	return
}
func (c *CoreClient) FallbackRouting(x context.Context, id, reason string) (r FinalizeRoutingResult, e error) {
	e = c.call(x, "fallback-routing", []string{"--run", id, "--reason", reason}, &r)
	return
}
func (c *CoreClient) ValidateCandidates(x context.Context, id, role, input string) (r ValidateCandidatesResult, e error) {
	e = c.call(x, "validate-candidates", []string{"--run", id, "--role", role, "--input", input}, &r)
	return
}
func (c *CoreClient) MarkReviewerFailed(x context.Context, id, role, reason string) error {
	return c.call(x, "mark-failed", []string{"--run", id, "--stage", "review", "--role", role, "--reason", reason}, nil)
}
func (c *CoreClient) PrepareEvidence(x context.Context, id string) (r PrepareEvidenceResult, e error) {
	e = c.call(x, "prepare-evidence", []string{"--run", id}, &r)
	return
}
func (c *CoreClient) ValidateVerdicts(x context.Context, id, input string) (r ValidateVerdictsResult, e error) {
	e = c.call(x, "validate-verdicts", []string{"--run", id, "--input", input}, &r)
	return
}
func (c *CoreClient) MarkEvidenceFailed(x context.Context, id, reason string) error {
	return c.call(x, "mark-failed", []string{"--run", id, "--stage", "evidence", "--reason", reason}, nil)
}
func (c *CoreClient) Aggregate(x context.Context, id string) (r AggregateResult, e error) {
	e = c.call(x, "aggregate", []string{"--run", id}, &r)
	return
}
func (c *CoreClient) Render(x context.Context, id string) (r RenderResult, e error) {
	e = c.call(x, "render", []string{"--run", id}, &r)
	return
}
func (c *CoreClient) Inspect(x context.Context, id string) (r InspectResult, e error) {
	e = c.call(x, "inspect", []string{"--run", id}, &r)
	return
}

func (c *CoreClient) call(ctx context.Context, operation string, args []string, out any) error {
	if c == nil || c.Path == "" || c.Runner == nil {
		return &CoreError{Kind: CoreErrorProcess, Op: operation}
	}
	limit := c.CoreTimeout
	if limit <= 0 {
		limit = budget.CoreCall
	}
	child, cancel, err := budget.Child(ctx, limit)
	if err != nil {
		return &CoreError{Kind: CoreErrorProcess, Op: operation}
	}
	defer cancel()
	deadline, _ := child.Deadline()
	argv := []string{"--error-format", "json"}
	if c.RunRoot != "" {
		argv = append(argv, "--run-root", c.RunRoot)
	}
	argv = append(argv, operation)
	argv = append(argv, args...)
	result, runErr := c.Runner.Run(child, process.Request{Path: c.Path, Args: argv, Env: append([]string(nil), c.Env...), Timeout: time.Until(deadline), OutputLimit: coreOutputLimit})
	if runErr != nil || result.TimedOut {
		return &CoreError{Kind: CoreErrorProcess, Op: operation, ExitCode: result.ExitCode}
	}
	if result.ExitCode != 0 {
		return coreFailure(operation, result)
	}
	if out == nil {
		return nil
	}
	if decodeOne(result.Stdout, out) != nil {
		return &CoreError{Kind: CoreErrorProtocol, Op: operation, ExitCode: result.ExitCode}
	}
	return nil
}
func coreFailure(operation string, result process.Result) error {
	var envelope ErrorEnvelope
	if decodeOne(result.Stderr, &envelope) == nil && envelope.Version == 1 && envelope.Operation == operation {
		switch envelope.Kind {
		case string(CoreErrorValidation):
			return &CoreError{Kind: CoreErrorValidation, Op: operation, ExitCode: result.ExitCode}
		case string(CoreErrorOperation):
			return &CoreError{Kind: CoreErrorOperation, Op: operation, ExitCode: result.ExitCode}
		}
	}
	return &CoreError{Kind: CoreErrorProcess, Op: operation, ExitCode: result.ExitCode}
}
func decodeOne(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON documents")
		}
		return err
	}
	return nil
}
