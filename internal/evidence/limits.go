package evidence

import "fmt"

const MaxCandidates = 250

func ValidateCandidateLimit(candidates CandidateSet) error {
	if len(candidates.Findings) > MaxCandidates {
		return fmt.Errorf("candidate count %d exceeds evidence gate limit %d", len(candidates.Findings), MaxCandidates)
	}
	return nil
}
