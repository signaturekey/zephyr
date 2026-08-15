package schema

type SemanticRoutingEnvelope struct {
	Version   int                       `json:"version"`
	RunID     string                    `json:"run_id"`
	Decisions []SemanticRoutingDecision `json:"decisions"`
}

type SemanticRoutingDecision struct {
	Role         string   `json:"role"`
	Decision     string   `json:"decision"`
	EvidenceRefs []string `json:"evidence_refs"`
	Reason       string   `json:"reason"`
	Confidence   float64  `json:"confidence"`
}
