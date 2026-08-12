package workflow

import (
	"time"

	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/gitcontext"
	"github.com/signaturekey/zephyr/internal/report"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/run"
)

type InitOptions struct {
	Repository  string
	Mode        run.Mode
	Source      run.Source
	BaseRef     string
	CommitRange string
	PlanPath    string
}

type InitResult struct {
	RunID    string `json:"run_id"`
	RunDir   string `json:"run_dir"`
	Manifest string `json:"manifest"`
}

type CollectOptions struct {
	RunID                   string
	IncludeGenerated        bool
	IncludeVendor           bool
	IncludeUntrackedContent bool
	MaxUntrackedBytes       int64
}

type CollectResult struct {
	RunID        string               `json:"run_id"`
	Mode         run.Mode             `json:"mode"`
	Source       run.Source           `json:"source"`
	Reviewable   bool                 `json:"reviewable_changes"`
	Stats        gitcontext.DiffStats `json:"stats"`
	SnapshotPath string               `json:"snapshot_path"`
	MetadataPath string               `json:"metadata_path"`
	StatusPath   string               `json:"status_path"`
}

type ContextAddOptions struct {
	RunID   string
	Source  string
	Key     string
	URL     string
	Content []byte
}

type ContextAddResult struct {
	RunID       string    `json:"run_id"`
	Path        string    `json:"path"`
	Source      string    `json:"source"`
	Key         string    `json:"key"`
	FetchedAt   time.Time `json:"fetched_at"`
	ContentHash string    `json:"content_hash"`
}

type CapabilitySource string

const (
	CapabilityJira       CapabilitySource = "jira"
	CapabilityConfluence CapabilitySource = "confluence"
	CapabilityBitbucket  CapabilitySource = "bitbucket"
)

type CapabilityStatus string

const (
	CapabilityAvailable   CapabilityStatus = "available"
	CapabilityUnavailable CapabilityStatus = "unavailable"
	CapabilityNotRequired CapabilityStatus = "not-required"
)

type CapabilitySetOptions struct {
	RunID  string
	Source CapabilitySource
	Status CapabilityStatus
	Reason string
}

type CapabilityRecord struct {
	Source CapabilitySource `json:"source"`
	Status CapabilityStatus `json:"status"`
	Reason string           `json:"reason,omitempty"`
}

type CapabilityDocument struct {
	Version      int                `json:"version"`
	RunID        string             `json:"run_id"`
	Capabilities []CapabilityRecord `json:"capabilities"`
}

type CoverageAddOptions struct {
	RunID           string
	Source          string
	Reason          string
	AllowAfterRoute bool
}

type CoverageDocument struct {
	Version int                         `json:"version"`
	RunID   string                      `json:"run_id"`
	Limits  []contextpack.CoverageLimit `json:"limits"`
}

type RouteOptions struct {
	RunID        string
	ForceInclude []string
	ForceExclude []string
}

type RouteResult struct {
	RunID              string                  `json:"run_id"`
	PacketPath         string                  `json:"packet_path"`
	RoutingRequestPath string                  `json:"routing_request_path"`
	RoutingPath        string                  `json:"routing_path,omitempty"`
	RoutingRequest     routing.SemanticRequest `json:"routing_request"`
	Routing            routing.Result          `json:"routing,omitempty"`
}

type ValidateRoutingOptions struct {
	RunID string
	Input []byte
}

type FinalizeRoutingOptions struct {
	RunID  string
	Reason string
}

type FinalizeRoutingResult struct {
	RunID        string         `json:"run_id"`
	RoutingPath  string         `json:"routing_path"`
	Routing      routing.Result `json:"routing"`
	TraceWarning string         `json:"trace_warning,omitempty"`
}

type ValidateCandidatesOptions struct {
	RunID string
	Role  string
	Input []byte
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

type ValidateVerdictsOptions struct {
	RunID string
	Input []byte
}

type ValidateVerdictsResult struct {
	RunID       string `json:"run_id"`
	Verdicts    int    `json:"verdicts"`
	VerdictPath string `json:"verdict_path"`
}

type MarkFailedOptions struct {
	RunID  string
	Stage  string
	Role   string
	Reason string
}

type MarkFailedResult struct {
	RunID string    `json:"run_id"`
	State run.State `json:"state"`
	Stage string    `json:"stage"`
	Role  string    `json:"role,omitempty"`
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

type ArtifactPaths struct {
	Manifest       string `json:"manifest"`
	Snapshot       string `json:"snapshot,omitempty"`
	Capabilities   string `json:"capabilities,omitempty"`
	Packet         string `json:"packet,omitempty"`
	Routing        string `json:"routing,omitempty"`
	RoutingRequest string `json:"routing_request,omitempty"`
	Candidates     string `json:"candidates,omitempty"`
	Verdicts       string `json:"verdicts,omitempty"`
	ReviewJSON     string `json:"review_json,omitempty"`
	ReviewMarkdown string `json:"review_markdown,omitempty"`
	Rejected       string `json:"rejected,omitempty"`
	Trace          string `json:"trace,omitempty"`
}

type InspectCounts struct {
	SelectedRoles     int            `json:"selected_roles"`
	ValidatedRoles    int            `json:"validated_roles"`
	FailedRoles       int            `json:"failed_roles"`
	ConfirmedFindings int            `json:"confirmed_findings"`
	NeedsHuman        int            `json:"needs_human"`
	BySeverity        map[string]int `json:"by_severity"`
}

type InspectResult struct {
	RunID          string                `json:"run_id"`
	RunDir         string                `json:"run_dir"`
	State          run.State             `json:"state"`
	Mode           run.Mode              `json:"mode"`
	Source         run.Source            `json:"source"`
	Stages         []run.Stage           `json:"stages"`
	Capabilities   []CapabilityRecord    `json:"capabilities"`
	CoverageLimits []string              `json:"coverage_limits"`
	Staleness      *gitcontext.Staleness `json:"staleness,omitempty"`
	Counts         InspectCounts         `json:"counts"`
	Artifacts      ArtifactPaths         `json:"artifacts"`
	Routing        *routing.Result       `json:"routing,omitempty"`
	Review         *report.Review        `json:"review,omitempty"`
}
