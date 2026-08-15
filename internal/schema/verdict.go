package schema

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
