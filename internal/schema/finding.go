package schema

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
