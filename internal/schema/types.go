package schema

const ProtocolVersion = 1

type Severity string

const (
	SeverityP0 Severity = "P0"
	SeverityP1 Severity = "P1"
	SeverityP2 Severity = "P2"
	SeverityP3 Severity = "P3"
)

func (severity Severity) Valid() bool {
	switch severity {
	case SeverityP0, SeverityP1, SeverityP2, SeverityP3:
		return true
	default:
		return false
	}
}

func (severity Severity) Rank() int {
	switch severity {
	case SeverityP0:
		return 0
	case SeverityP1:
		return 1
	case SeverityP2:
		return 2
	case SeverityP3:
		return 3
	default:
		return 4
	}
}

type CandidateEnvelope struct {
	Version  int                `json:"version"`
	RunID    string             `json:"run_id"`
	Role     string             `json:"role"`
	Findings []CandidateFinding `json:"findings"`
}

type CandidateFinding struct {
	ID             string          `json:"id"`
	Role           string          `json:"role"`
	Severity       Severity        `json:"severity"`
	Category       string          `json:"category"`
	Title          string          `json:"title"`
	Location       FindingLocation `json:"location"`
	Evidence       FindingEvidence `json:"evidence"`
	Impact         string          `json:"impact"`
	Recommendation string          `json:"recommendation"`
	Confidence     float64         `json:"confidence"`
	NeedsHuman     bool            `json:"needs_human"`
}

type FindingLocation struct {
	File      string `json:"file,omitempty"`
	Artifact  string `json:"artifact,omitempty"`
	Section   string `json:"section,omitempty"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
}

func (location FindingLocation) IsCode() bool { return location.File != "" }

func (location FindingLocation) IsArtifact() bool { return location.Artifact != "" }

type FindingEvidence struct {
	Code              *string `json:"code"`
	ExecutionPath     string  `json:"execution_path"`
	ViolatedInvariant string  `json:"violated_invariant"`
	RequirementSource *string `json:"requirement_source"`
	FalsifierChecked  string  `json:"falsifier_checked"`
}

type Verdict string

const (
	VerdictAccepted   Verdict = "accepted"
	VerdictRejected   Verdict = "rejected"
	VerdictDowngraded Verdict = "downgraded"
	VerdictDuplicate  Verdict = "duplicate"
	VerdictNeedsHuman Verdict = "needs-human"
)

func (verdict Verdict) Valid() bool {
	switch verdict {
	case VerdictAccepted, VerdictRejected, VerdictDowngraded, VerdictDuplicate, VerdictNeedsHuman:
		return true
	default:
		return false
	}
}

type EvidenceVerdictEnvelope struct {
	Version  int               `json:"version"`
	RunID    string            `json:"run_id"`
	Verdicts []EvidenceVerdict `json:"verdicts"`
}

type EvidenceVerdict struct {
	CandidateID   string    `json:"candidate_id"`
	Verdict       Verdict   `json:"verdict"`
	FinalSeverity *Severity `json:"final_severity"`
	ReasonCode    string    `json:"reason_code"`
	Reason        string    `json:"reason"`
	DuplicateOf   *string   `json:"duplicate_of"`
}
