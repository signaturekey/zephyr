package report

import (
	"time"

	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/schema"
)

const Version = 1

type Scope struct {
	Mode              string             `json:"mode"`
	Source            string             `json:"source"`
	Repository        string             `json:"repository"`
	Branch            string             `json:"branch,omitempty"`
	Head              string             `json:"head,omitempty"`
	BaseRef           string             `json:"base_ref,omitempty"`
	BaseSHA           string             `json:"base_sha,omitempty"`
	TargetSHA         string             `json:"target_sha,omitempty"`
	MergeBase         string             `json:"merge_base,omitempty"`
	CommitRange       string             `json:"commit_range,omitempty"`
	ChangedFiles      []string           `json:"changed_files"`
	Plan              string             `json:"plan,omitempty"`
	PlanHash          string             `json:"plan_hash,omitempty"`
	ModelPolicySHA256 string             `json:"model_policy_sha256,omitempty"`
	Sources           []SourceProvenance `json:"sources"`
	Stale             bool               `json:"stale"`
}

type SourceProvenance struct {
	Source      string    `json:"source"`
	Key         string    `json:"key,omitempty"`
	URL         string    `json:"url,omitempty"`
	ContentHash string    `json:"content_hash"`
	FetchedAt   time.Time `json:"fetched_at,omitempty"`
}

type RoleDecision struct {
	Role    string   `json:"role"`
	Reasons []string `json:"reasons"`
}

type RoutingSummary struct {
	Profile     string         `json:"profile"`
	Selected    []RoleDecision `json:"selected"`
	Excluded    []RoleDecision `json:"excluded"`
	MaxParallel int            `json:"max_parallel"`
}

type FinalFinding struct {
	Candidate    schema.CandidateFinding `json:"candidate"`
	SourceRoles  []string                `json:"source_roles"`
	DuplicateIDs []string                `json:"duplicate_ids"`
	GateReason   string                  `json:"gate_reason"`
}

type HumanQuestion struct {
	Candidate schema.CandidateFinding `json:"candidate"`
	Reason    string                  `json:"reason"`
}

type RejectedCandidate struct {
	CandidateID string `json:"candidate_id"`
	Role        string `json:"role,omitempty"`
	Stage       string `json:"stage"`
	ReasonCode  string `json:"reason_code"`
	Reason      string `json:"reason"`
}

type RejectedSummary struct {
	Count    int            `json:"count"`
	ByReason map[string]int `json:"by_reason"`
	Path     string         `json:"path"`
}

type Review struct {
	Version        int             `json:"version"`
	RunID          string          `json:"run_id"`
	GeneratedAt    time.Time       `json:"generated_at"`
	Status         string          `json:"status"`
	Scope          Scope           `json:"scope"`
	Routing        RoutingSummary  `json:"routing"`
	Findings       []FinalFinding  `json:"findings"`
	NeedsHuman     []HumanQuestion `json:"needs_human"`
	CoverageLimits []string        `json:"coverage_limits"`
	Rejected       RejectedSummary `json:"rejected_candidates"`
}

type RejectedArtifact struct {
	Version  int                 `json:"version"`
	RunID    string              `json:"run_id"`
	Rejected []RejectedCandidate `json:"rejected"`
}

type AggregateInput struct {
	RunID           string
	GeneratedAt     time.Time
	Scope           Scope
	Routing         RoutingSummary
	Candidates      evidence.CandidateSet
	Verdicts        schema.EvidenceVerdictEnvelope
	PrecheckReports []evidence.PrecheckReport
	CoverageLimits  []string
	RejectedPath    string
}
