package routing

import (
	"errors"
	"strings"
	"testing"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/schema"
)

func TestSemanticRoutingCanRemoveWeakTextMatches(t *testing.T) {
	cfg := mustConfig(t, nil)
	request, err := PrepareSemantic(cfg, Input{
		Mode: ModePlan, HasPlan: true, Signals: []string{"architecture", "contract", "frontend", "skill-authoring", "sql"},
	}, "run-1", strings.Repeat("a", 64), []EvidenceSource{{ID: "scope", Kind: "scope", Source: "packet"}, {ID: "plan", Kind: "plan", Source: "spec.md"}, {ID: "business-001", Kind: "business-context", Source: "bitbucket:master"}})
	if err != nil {
		t.Fatal(err)
	}
	if !decisionProtected(request.Protected, config.RoleArchitectReviewer) {
		t.Fatalf("mandatory architect is not protected: %#v", request.Protected)
	}

	proposal := semanticProposal(request, map[string]bool{
		config.RoleContractReviewer:     true,
		config.RoleSkillAuthoringExpert: true,
	})
	result, err := ResolveSemantic(request, proposal)
	if err != nil {
		t.Fatal(err)
	}
	if decisionPresent(result.Selected, config.RoleFrontendExpert) || decisionPresent(result.Selected, config.RoleSQLExpert) {
		t.Fatalf("weak false positives remained selected: %#v", result.Selected)
	}
	for _, role := range []string{config.RoleArchitectReviewer, config.RoleContractReviewer, config.RoleSkillAuthoringExpert} {
		if !decisionPresent(result.Selected, role) {
			t.Fatalf("expected role %q missing from %#v", role, result.Selected)
		}
	}
}

func TestSemanticRoutingCannotRemovePathProtectedRole(t *testing.T) {
	request, err := PrepareSemantic(mustConfig(t, nil), Input{
		Mode: ModeImplementation, HasChanges: true,
		ChangedPaths: []string{"src/view.tsx", "migrations/001.sql"}, Signals: []string{"frontend", "typescript", "sql"}, StrongSignals: []string{"frontend", "typescript", "sql"},
		ForceInclude: []string{config.RoleQAExpert},
	}, "run-2", strings.Repeat("b", 64), []EvidenceSource{{ID: "scope", Kind: "scope", Source: "packet"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{config.RoleCodeReviewer, config.RoleSQLExpert, config.RoleTypeScriptExpert, config.RoleFrontendExpert, config.RoleQAExpert} {
		if !decisionProtected(request.Protected, role) {
			t.Fatalf("path role %q is not protected: %#v", role, request.Protected)
		}
	}
	proposal := semanticProposal(request, nil)
	proposal.Decisions = append(proposal.Decisions, schema.SemanticRoutingDecision{
		Role: config.RoleFrontendExpert, Decision: "exclude", EvidenceRefs: []string{"scope"}, Reason: "attempted override", Confidence: 1,
	})
	if _, err := ResolveSemantic(request, proposal); !errors.Is(err, ErrInvalidSemanticDecision) {
		t.Fatalf("protected override error = %v", err)
	}
}

func TestSemanticRoutingProtectsPythonPathRole(t *testing.T) {
	request, err := PrepareSemantic(mustConfig(t, nil), Input{
		Mode: ModeImplementation, HasChanges: true,
		ChangedPaths: []string{"services/payments/worker.py"}, StrongSignals: []string{"python"},
	}, "run-python", strings.Repeat("a", 64), []EvidenceSource{{ID: "scope", Kind: "scope", Source: "packet"}})
	if err != nil {
		t.Fatal(err)
	}
	if !decisionProtected(request.Protected, config.RolePythonExpert) {
		t.Fatalf("python path role is not protected: %#v", request.Protected)
	}
	result, err := FallbackSemantic(request, "router unavailable")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Selected) != len(config.KnownRoles()) || !decisionPresent(result.Selected, config.RolePythonExpert) {
		t.Fatalf("fallback omitted python expert: %#v", result.Selected)
	}
}

func TestSemanticRoutingProtectsSecurityAuditorFromUntrustedDiff(t *testing.T) {
	request, err := PrepareSemantic(mustConfig(t, nil), Input{
		Mode: ModeImplementation, HasChanges: true, ChangedPaths: []string{"internal/harmless.go"},
	}, "run-security", strings.Repeat("e", 64), []EvidenceSource{{ID: "diff", Kind: "diff", Source: "diff.full"}})
	if err != nil {
		t.Fatal(err)
	}
	if !decisionProtected(request.Protected, config.RoleSecurityAuditor) {
		t.Fatalf("security-auditor must be protected for implementation review: %#v", request.Protected)
	}
	proposal := semanticProposal(request, nil)
	proposal.Decisions = append(proposal.Decisions, schema.SemanticRoutingDecision{
		Role: config.RoleSecurityAuditor, Decision: "exclude", EvidenceRefs: []string{"diff"}, Reason: "untrusted override", Confidence: 1,
	})
	if _, err := ResolveSemantic(request, proposal); !errors.Is(err, ErrInvalidSemanticDecision) {
		t.Fatalf("security override error = %v", err)
	}
}

func TestSemanticRoutingRejectsIncompleteOrUnknownEvidence(t *testing.T) {
	request, err := PrepareSemantic(mustConfig(t, nil), Input{Mode: ModePlan, HasPlan: true}, "run-3", strings.Repeat("c", 64), []EvidenceSource{{ID: "scope", Kind: "scope", Source: "packet"}})
	if err != nil {
		t.Fatal(err)
	}
	proposal := semanticProposal(request, nil)
	proposal.Decisions[0].EvidenceRefs = []string{"missing"}
	if _, err := ResolveSemantic(request, proposal); !errors.Is(err, ErrInvalidSemanticDecision) {
		t.Fatalf("unknown evidence error = %v", err)
	}
	proposal = semanticProposal(request, nil)
	proposal.Decisions = proposal.Decisions[:len(proposal.Decisions)-1]
	if _, err := ResolveSemantic(request, proposal); !errors.Is(err, ErrInvalidSemanticDecision) {
		t.Fatalf("incomplete decision error = %v", err)
	}
	tampered := request
	tampered.Candidates = append([]SemanticCandidate{}, request.Candidates...)
	tampered.Candidates[0].Role = "unknown-role"
	if _, err := FallbackSemantic(tampered, "fixture"); !errors.Is(err, ErrInvalidSemanticDecision) {
		t.Fatalf("tampered request error = %v", err)
	}
}

func TestSemanticFallbackIncludesEveryUnresolvedCandidate(t *testing.T) {
	request, err := PrepareSemantic(mustConfig(t, []byte("version: 1\nlimits:\n  max_roles_standard: 2\n")), Input{Mode: ModePlan, HasPlan: true}, "run-4", strings.Repeat("d", 64), []EvidenceSource{{ID: "scope", Kind: "scope", Source: "packet"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := FallbackSemantic(request, "router unavailable")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Degraded || result.Resolution != "deterministic-fallback" || len(result.Selected) != len(config.KnownRoles()) {
		t.Fatalf("unexpected fallback result: %#v", result)
	}
}

func TestSemanticRoutingDeduplicatesEvidenceReferences(t *testing.T) {
	request, err := PrepareSemantic(mustConfig(t, nil), Input{Mode: ModePlan, HasPlan: true}, "run-refs", strings.Repeat("f", 64), []EvidenceSource{{ID: "scope", Kind: "scope", Source: "packet"}})
	if err != nil {
		t.Fatal(err)
	}
	proposal := semanticProposal(request, nil)
	proposal.Decisions[0].EvidenceRefs = []string{"scope", "scope"}
	result, err := ResolveSemantic(request, proposal)
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range result.Excluded {
		if decision.Role != proposal.Decisions[0].Role {
			continue
		}
		refs := decision.Reasons[len(decision.Reasons)-1].EvidenceRefs
		if len(refs) != 1 || refs[0] != "scope" {
			t.Fatalf("evidence refs were not normalized: %v", refs)
		}
		return
	}
	t.Fatalf("decision %q not found in excluded roles", proposal.Decisions[0].Role)
}

func TestDeterministicFinalizationDoesNotClaimLLMResolution(t *testing.T) {
	excluded := make([]string, 0, len(config.KnownRoles())-1)
	for _, role := range config.KnownRoles() {
		if role != config.RoleCodeReviewer {
			excluded = append(excluded, role)
		}
	}
	request, err := PrepareSemantic(mustConfig(t, nil), Input{
		Mode: ModeImplementation, HasChanges: true, ForceExclude: excluded,
	}, "run-deterministic", strings.Repeat("1", 64), []EvidenceSource{{ID: "scope", Kind: "scope", Source: "packet"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Candidates) != 0 {
		t.Fatalf("semantic candidates = %d, want 0", len(request.Candidates))
	}
	result, err := ResolveDeterministic(request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Resolution != "deterministic-only" || result.Degraded {
		t.Fatalf("unexpected deterministic resolution: %#v", result)
	}
}

func semanticProposal(request SemanticRequest, included map[string]bool) schema.SemanticRoutingEnvelope {
	result := schema.SemanticRoutingEnvelope{Version: SemanticRoutingVersion, RunID: request.RunID, Decisions: []schema.SemanticRoutingDecision{}}
	for _, candidate := range request.Candidates {
		decision := "exclude"
		if included[candidate.Role] {
			decision = "include"
		}
		result.Decisions = append(result.Decisions, schema.SemanticRoutingDecision{
			Role: candidate.Role, Decision: decision, EvidenceRefs: []string{"scope"}, Reason: "fixture decision", Confidence: 0.9,
		})
	}
	return result
}

func decisionPresent(decisions []Decision, role string) bool {
	for _, decision := range decisions {
		if decision.Role == role {
			return true
		}
	}
	return false
}

func decisionProtected(decisions []Decision, role string) bool {
	for _, decision := range decisions {
		if decision.Role == role {
			return decision.Protected
		}
	}
	return false
}
