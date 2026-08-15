package routing

import (
	"testing"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareProtectsRequiredPathAndSecurityRolesWithoutCoverageLimit(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	require.NoError(t, err)
	request, err := Prepare(cfg, Input{RunID: "run", ChangedPaths: []string{"cmd/main.go"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"code-reviewer", "golang-expert", "security-auditor"}, roles(request.Protected))
	assert.Len(t, request.Protected, 3)
	assert.Len(t, request.Candidates, len(config.KnownRoles())-3)
}

func TestResolveAndFallbackAccountForEveryOptionalRole(t *testing.T) {
	cfg, err := config.LoadBytes(nil)
	require.NoError(t, err)
	request, err := Prepare(cfg, Input{RunID: "run", ChangedPaths: []string{"README.md"}})
	require.NoError(t, err)
	proposal := schema.SemanticRoutingEnvelope{Version: 1, RunID: "run"}
	for _, candidate := range request.Candidates {
		proposal.Decisions = append(proposal.Decisions, schema.SemanticRoutingDecision{
			Role: candidate.Role, Decision: "exclude", EvidenceRefs: []string{"snapshot.diff"}, Reason: "scope is unrelated", Confidence: 1,
		})
	}
	result, err := Resolve(request, proposal)
	require.NoError(t, err)
	assert.Len(t, result.Selected, len(request.Protected))
	assert.Len(t, result.Excluded, len(request.Excluded)+len(request.Candidates))

	fallback := Fallback(request, "router failed")
	assert.Len(t, fallback.Selected, len(request.Protected)+len(request.Candidates))
	assert.True(t, fallback.Degraded)
}

func TestDetectReactSignalRequiresDiffEvidence(t *testing.T) {
	signals, strong := DetectSignals([]string{"src/widget.tsx"}, `+import React from "react"`)
	assert.Contains(t, signals, "react")
	assert.Contains(t, strong, "react")

	signals, _ = DetectSignals([]string{"src/widget.tsx"}, "+export const value = 1")
	assert.NotContains(t, signals, "react")
}

func TestDetectSignalsDoesNotTreatPackageAsMessageAcknowledgement(t *testing.T) {
	signals, _ := DetectSignals([]string{"divide.go"}, "+package divide\n+return left / right, nil")
	assert.NotContains(t, signals, "messaging")
}

func roles(decisions []Decision) []string {
	result := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		result = append(result, decision.Role)
	}
	return result
}
