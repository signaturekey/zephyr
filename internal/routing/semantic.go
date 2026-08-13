package routing

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/schema"
)

const SemanticRoutingVersion = 1

var ErrInvalidSemanticDecision = errors.New("invalid semantic routing decision")

type EvidenceSource struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	ContentHash string `json:"content_hash,omitempty"`
}

type SemanticCandidate struct {
	Role    string   `json:"role"`
	Scope   string   `json:"scope"`
	Reasons []Reason `json:"reasons"`
}

type SemanticRequest struct {
	Version         int                 `json:"version"`
	RunID           string              `json:"run_id"`
	PacketSHA256    string              `json:"packet_sha256"`
	Mode            Mode                `json:"mode"`
	Profile         config.Profile      `json:"profile"`
	Limit           int                 `json:"limit"`
	Protected       []Decision          `json:"protected"`
	Candidates      []SemanticCandidate `json:"candidates"`
	Excluded        []Decision          `json:"excluded"`
	EvidenceSources []EvidenceSource    `json:"evidence_sources"`
}

func PrepareSemantic(cfg config.Config, input Input, runID, packetSHA256 string, evidenceSources []EvidenceSource) (SemanticRequest, error) {
	mode, limit, states, err := classifyRoles(cfg, input)
	if err != nil {
		return SemanticRequest{}, err
	}

	request := SemanticRequest{
		Version:         SemanticRoutingVersion,
		RunID:           runID,
		PacketSHA256:    packetSHA256,
		Mode:            mode,
		Profile:         cfg.Profile,
		Limit:           limit,
		Protected:       []Decision{},
		Candidates:      []SemanticCandidate{},
		Excluded:        []Decision{},
		EvidenceSources: append([]EvidenceSource{}, evidenceSources...),
	}

	for _, role := range config.KnownRoles() {
		state := states[role]
		if state.excluded {
			request.Excluded = append(request.Excluded, Decision{
				Role: role, Source: exclusionSource(state), Reasons: cloneReasons(state.reasons),
			})
			continue
		}
		if securityProtected(mode, role) {
			state.reasons = append(state.reasons, Reason{
				Code:   ReasonSecurityBoundary,
				Detail: "security-auditor is protected for reviews of untrusted implementation changes",
			})
		}
		if semanticProtected(state, mode) {
			request.Protected = append(request.Protected, Decision{
				Role: role, Protected: true, Source: protectedSource(state, mode), Reasons: cloneReasons(state.reasons),
			})
			continue
		}
		request.Candidates = append(request.Candidates, semanticCandidate(state))
	}

	if len(request.Protected) > request.Limit {
		return SemanticRequest{}, fmt.Errorf("%w: %d protected roles exceed %s profile limit %d", ErrInvalidInput, len(request.Protected), request.Profile, request.Limit)
	}
	sort.Slice(request.Candidates, func(i, j int) bool {
		return rolePriority(request.Candidates[i].Role) < rolePriority(request.Candidates[j].Role)
	})
	sort.Slice(request.Excluded, func(i, j int) bool {
		return rolePriority(request.Excluded[i].Role) < rolePriority(request.Excluded[j].Role)
	})
	sort.Slice(request.EvidenceSources, func(i, j int) bool { return request.EvidenceSources[i].ID < request.EvidenceSources[j].ID })
	return request, nil
}

func ResolveDeterministic(request SemanticRequest) (Result, error) {
	if err := validateSemanticRequest(request); err != nil {
		return Result{}, err
	}
	if len(request.Candidates) != 0 {
		return Result{}, fmt.Errorf("%w: deterministic finalization requires zero semantic candidates", ErrInvalidSemanticDecision)
	}
	return buildSemanticResult(request, nil, nil, "deterministic-only", false, "")
}

func ResolveSemantic(request SemanticRequest, proposal schema.SemanticRoutingEnvelope) (Result, error) {
	if err := validateSemanticRequest(request); err != nil {
		return Result{}, err
	}
	if proposal.Version != SemanticRoutingVersion || proposal.RunID != request.RunID {
		return Result{}, fmt.Errorf("%w: semantic routing envelope does not match request", ErrInvalidSemanticDecision)
	}

	candidates := make(map[string]SemanticCandidate, len(request.Candidates))
	for _, candidate := range request.Candidates {
		candidates[candidate.Role] = candidate
	}
	evidence := make(map[string]struct{}, len(request.EvidenceSources))
	for _, source := range request.EvidenceSources {
		evidence[source.ID] = struct{}{}
	}
	decisions := make(map[string]schema.SemanticRoutingDecision, len(proposal.Decisions))
	for _, decision := range proposal.Decisions {
		if _, ok := candidates[decision.Role]; !ok {
			return Result{}, fmt.Errorf("%w: role %q is not a semantic candidate", ErrInvalidSemanticDecision, decision.Role)
		}
		if _, duplicate := decisions[decision.Role]; duplicate {
			return Result{}, fmt.Errorf("%w: duplicate decision for role %q", ErrInvalidSemanticDecision, decision.Role)
		}
		if decision.Decision != "include" && decision.Decision != "exclude" {
			return Result{}, fmt.Errorf("%w: role %q has unknown decision %q", ErrInvalidSemanticDecision, decision.Role, decision.Decision)
		}
		if strings.TrimSpace(decision.Reason) == "" || len(decision.EvidenceRefs) == 0 || decision.Confidence < 0 || decision.Confidence > 1 {
			return Result{}, fmt.Errorf("%w: role %q has an incomplete decision", ErrInvalidSemanticDecision, decision.Role)
		}
		seenRefs := make(map[string]struct{}, len(decision.EvidenceRefs))
		uniqueRefs := make([]string, 0, len(decision.EvidenceRefs))
		for _, reference := range decision.EvidenceRefs {
			if _, duplicate := seenRefs[reference]; duplicate {
				continue
			}
			seenRefs[reference] = struct{}{}
			if _, ok := evidence[reference]; !ok {
				return Result{}, fmt.Errorf("%w: role %q references unknown evidence %q", ErrInvalidSemanticDecision, decision.Role, reference)
			}
			uniqueRefs = append(uniqueRefs, reference)
		}
		decision.EvidenceRefs = uniqueRefs
		decisions[decision.Role] = decision
	}
	if len(decisions) != len(candidates) {
		return Result{}, fmt.Errorf("%w: got %d decisions for %d semantic candidates", ErrInvalidSemanticDecision, len(decisions), len(candidates))
	}

	included := make(map[string]Reason)
	excluded := make(map[string]Reason)
	for _, candidate := range request.Candidates {
		decision := decisions[candidate.Role]
		confidence := decision.Confidence
		reason := Reason{
			Detail:       strings.TrimSpace(decision.Reason),
			EvidenceRefs: append([]string{}, decision.EvidenceRefs...),
			Confidence:   &confidence,
		}
		if decision.Decision == "include" {
			reason.Code = ReasonSemanticIncluded
			included[candidate.Role] = reason
		} else {
			reason.Code = ReasonSemanticExcluded
			excluded[candidate.Role] = reason
		}
	}
	return buildSemanticResult(request, included, excluded, "semantic-llm", false, "")
}

func FallbackSemantic(request SemanticRequest, reason string) (Result, error) {
	if err := validateSemanticRequest(request); err != nil {
		return Result{}, err
	}
	included := make(map[string]Reason, len(request.Candidates))
	for _, candidate := range request.Candidates {
		included[candidate.Role] = Reason{
			Code:   ReasonSemanticFallback,
			Detail: "semantic routing was unresolved; conservative fallback included the role",
		}
	}
	request.Limit = len(config.KnownRoles())
	return buildSemanticResult(request, included, nil, "deterministic-fallback", true, strings.TrimSpace(reason))
}

func buildSemanticResult(request SemanticRequest, included, semanticExcluded map[string]Reason, resolution string, degraded bool, fallbackReason string) (Result, error) {
	selected := append([]Decision{}, request.Protected...)
	excluded := append([]Decision{}, request.Excluded...)
	for _, candidate := range request.Candidates {
		if reason, ok := included[candidate.Role]; ok {
			selected = append(selected, Decision{
				Role: candidate.Role, Source: resolution,
				Reasons: append(cloneReasons(candidate.Reasons), reason),
			})
			continue
		}
		reason, ok := semanticExcluded[candidate.Role]
		if !ok {
			return Result{}, fmt.Errorf("%w: unresolved role %q", ErrInvalidSemanticDecision, candidate.Role)
		}
		excluded = append(excluded, Decision{
			Role: candidate.Role, Source: resolution,
			Reasons: append(cloneReasons(candidate.Reasons), reason),
		})
	}

	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].Protected != selected[j].Protected {
			return selected[i].Protected
		}
		return rolePriority(selected[i].Role) < rolePriority(selected[j].Role)
	})
	if len(selected) > request.Limit {
		for _, displaced := range selected[request.Limit:] {
			displaced.Source = "profile-limit"
			displaced.Reasons = append(displaced.Reasons, Reason{
				Code:   ReasonProfileLimit,
				Detail: fmt.Sprintf("role was displaced by protected or higher-priority roles at the %s profile limit of %d", request.Profile, request.Limit),
			})
			excluded = append(excluded, displaced)
		}
		selected = selected[:request.Limit]
	}
	sort.Slice(excluded, func(i, j int) bool { return rolePriority(excluded[i].Role) < rolePriority(excluded[j].Role) })
	return Result{
		Mode: request.Mode, Profile: request.Profile, Limit: request.Limit,
		Resolution: resolution, Degraded: degraded, FallbackReason: fallbackReason,
		Selected: selected, Excluded: excluded,
	}, nil
}

func validateSemanticRequest(request SemanticRequest) error {
	if request.Version != SemanticRoutingVersion || strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.PacketSHA256) == "" {
		return fmt.Errorf("%w: malformed semantic request identity", ErrInvalidSemanticDecision)
	}
	if _, err := hex.DecodeString(request.PacketSHA256); err != nil || len(request.PacketSHA256) != 64 {
		return fmt.Errorf("%w: malformed packet SHA-256", ErrInvalidSemanticDecision)
	}
	if request.Mode != ModePlan && request.Mode != ModeImplementation && request.Mode != ModeAlignment {
		return fmt.Errorf("%w: unsupported mode %q", ErrInvalidSemanticDecision, request.Mode)
	}
	if request.Profile != config.ProfileStandard && request.Profile != config.ProfileThorough {
		return fmt.Errorf("%w: unsupported profile %q", ErrInvalidSemanticDecision, request.Profile)
	}
	if request.Limit <= 0 || request.Limit > len(config.KnownRoles()) {
		return fmt.Errorf("%w: invalid role limit %d", ErrInvalidSemanticDecision, request.Limit)
	}
	knownRoles := make(map[string]struct{}, len(config.KnownRoles()))
	for _, role := range config.KnownRoles() {
		knownRoles[role] = struct{}{}
	}
	seenRoles := make(map[string]struct{}, len(config.KnownRoles()))
	for _, decision := range request.Protected {
		if _, known := knownRoles[decision.Role]; !known {
			return fmt.Errorf("%w: unknown protected role %q", ErrInvalidSemanticDecision, decision.Role)
		}
		if !decision.Protected {
			return fmt.Errorf("%w: protected role %q is not marked protected", ErrInvalidSemanticDecision, decision.Role)
		}
		if _, duplicate := seenRoles[decision.Role]; duplicate {
			return fmt.Errorf("%w: duplicate role %q", ErrInvalidSemanticDecision, decision.Role)
		}
		seenRoles[decision.Role] = struct{}{}
	}
	for _, candidate := range request.Candidates {
		if _, known := knownRoles[candidate.Role]; !known || strings.TrimSpace(candidate.Scope) == "" {
			return fmt.Errorf("%w: invalid semantic candidate %q", ErrInvalidSemanticDecision, candidate.Role)
		}
		if _, duplicate := seenRoles[candidate.Role]; duplicate {
			return fmt.Errorf("%w: duplicate role %q", ErrInvalidSemanticDecision, candidate.Role)
		}
		seenRoles[candidate.Role] = struct{}{}
	}
	for _, decision := range request.Excluded {
		if _, known := knownRoles[decision.Role]; !known {
			return fmt.Errorf("%w: unknown excluded role %q", ErrInvalidSemanticDecision, decision.Role)
		}
		if _, duplicate := seenRoles[decision.Role]; duplicate {
			return fmt.Errorf("%w: duplicate role %q", ErrInvalidSemanticDecision, decision.Role)
		}
		seenRoles[decision.Role] = struct{}{}
	}
	if len(seenRoles) != len(config.KnownRoles()) {
		return fmt.Errorf("%w: semantic request accounts for %d of %d roles", ErrInvalidSemanticDecision, len(seenRoles), len(config.KnownRoles()))
	}
	for role := range knownRoles {
		if _, ok := seenRoles[role]; !ok {
			return fmt.Errorf("%w: role %q is unaccounted", ErrInvalidSemanticDecision, role)
		}
	}
	seenEvidence := make(map[string]struct{}, len(request.EvidenceSources))
	for _, source := range request.EvidenceSources {
		if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Kind) == "" || strings.TrimSpace(source.Source) == "" {
			return fmt.Errorf("%w: empty evidence source ID", ErrInvalidSemanticDecision)
		}
		if _, duplicate := seenEvidence[source.ID]; duplicate {
			return fmt.Errorf("%w: duplicate evidence source %q", ErrInvalidSemanticDecision, source.ID)
		}
		seenEvidence[source.ID] = struct{}{}
	}
	if len(seenEvidence) == 0 {
		return fmt.Errorf("%w: semantic request has no evidence sources", ErrInvalidSemanticDecision)
	}
	return nil
}

func semanticProtected(state *roleState, mode Mode) bool {
	return state.required || state.forced || state.matchedPath || state.matchedStrongSignal || securityProtected(mode, state.role)
}

func securityProtected(mode Mode, role string) bool {
	return role == config.RoleSecurityAuditor && (mode == ModeImplementation || mode == ModeAlignment)
}

func semanticCandidate(state *roleState) SemanticCandidate {
	reasons := cloneReasons(state.reasons)
	if len(reasons) == 0 {
		reasons = []Reason{{Code: ReasonSemanticCandidate, Detail: "optional role requires semantic scope classification"}}
	}
	return SemanticCandidate{Role: state.role, Scope: roleScope(state.role), Reasons: reasons}
}

func intersects(left []string, set map[string]struct{}) bool {
	for _, value := range left {
		if _, ok := set[value]; ok {
			return true
		}
	}
	return false
}

func protectedSource(state *roleState, mode Mode) string {
	switch {
	case state.required:
		return "mode"
	case state.forced:
		return "user"
	case state.matchedPath:
		return "path"
	case state.matchedStrongSignal:
		return "strong-signal"
	case securityProtected(mode, state.role):
		return "security-policy"
	default:
		return "deterministic"
	}
}

func exclusionSource(state *roleState) string {
	for _, reason := range state.reasons {
		if reason.Code == ReasonDisabled {
			return "config"
		}
		if reason.Code == ReasonExplicitExclusion {
			return "user"
		}
	}
	return "deterministic"
}

func roleScope(role string) string {
	scopes := map[string]string{
		config.RoleCodeReviewer:         "functional correctness of implementation changes",
		config.RoleArchitectReviewer:    "architecture, ownership, compatibility, rollout, and specification completeness",
		config.RoleGolangExpert:         "Go runtime, concurrency, errors, resources, and API semantics",
		config.RolePythonExpert:         "Python runtime, async, exceptions, types, mutability, resources, and import semantics",
		config.RoleTypeScriptExpert:     "TypeScript type and runtime contract safety",
		config.RoleFrontendExpert:       "browser UI, React lifecycle, accessibility, and user-visible behavior",
		config.RoleSkillAuthoringExpert: "SKILL.md structure, triggering, references, workflow safety, and eval coverage",
		config.RoleReliabilityExpert:    "timeouts, retries, idempotency, backpressure, degradation, and observability",
		config.RoleMessagingExpert:      "queue and stream delivery, ordering, acknowledgement, retry, and DLQ semantics",
		config.RoleInfrastructureExpert: "Docker, Kubernetes, Helm, CI/CD, probes, resources, rollout, and rollback",
		config.RoleStorageExpert:        "cache, search, object storage, consistency, invalidation, retention, and capacity",
		config.RoleSecurityAuditor:      "authentication, authorization, injection, secrets, PII, and privilege boundaries",
		config.RoleSQLExpert:            "SQL, transactions, locking, indexes, and database migration safety",
		config.RoleContractReviewer:     "OpenAPI, Proto, schemas, public DTOs, events, and compatibility",
		config.RoleQAExpert:             "specific changed-behavior, boundary, negative, and failure-mode test coverage",
		config.RoleCodeSimplifier:       "demonstrable changed-code maintenance risk from avoidable complexity",
	}
	return scopes[role]
}
