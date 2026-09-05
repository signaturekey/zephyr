package evidence

import (
	"testing"

	"github.com/signaturekey/zephyr/internal/protocol"
	"github.com/stretchr/testify/assert"
)

func TestValidateCandidateLimit(t *testing.T) {
	for _, test := range []struct {
		name  string
		count int
		valid bool
	}{
		{name: "maximum", count: MaxCandidates, valid: true},
		{name: "above maximum", count: MaxCandidates + 1, valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidates := CandidateSet{Findings: make([]protocol.CandidateFinding, test.count)}
			if test.valid {
				assert.NoError(t, ValidateCandidateLimit(candidates))
			} else {
				assert.ErrorContains(t, ValidateCandidateLimit(candidates), "exceeds evidence gate limit")
			}
		})
	}
}
