package routing

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/protocol"
)

const Version = 1

var ErrInvalid = errors.New("invalid routing input")

type Input struct {
	RunID         string
	ChangedPaths  []string
	Signals       []string
	StrongSignals []string
	Include       []string
	Exclude       []string
	Evidence      []EvidenceSource
}

type EvidenceSource struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

type Candidate struct {
	Role  string `json:"role"`
	Scope string `json:"scope"`
}

type Decision struct {
	Role      string   `json:"role"`
	Protected bool     `json:"protected,omitempty"`
	Source    string   `json:"source"`
	Reasons   []string `json:"reasons"`
}

type Request struct {
	Version         int              `json:"version"`
	RunID           string           `json:"run_id"`
	Protected       []Decision       `json:"protected"`
	Candidates      []Candidate      `json:"candidates"`
	Excluded        []Decision       `json:"excluded"`
	EvidenceSources []EvidenceSource `json:"evidence_sources"`
}

type Result struct {
	Selected       []Decision `json:"selected"`
	Excluded       []Decision `json:"excluded"`
	Resolution     string     `json:"resolution"`
	Degraded       bool       `json:"degraded"`
	FallbackReason string     `json:"fallback_reason,omitempty"`
}

func Prepare(cfg config.Config, input Input) (Request, error) {
	if strings.TrimSpace(input.RunID) == "" {
		return Request{}, fmt.Errorf("%w: run ID is required", ErrInvalid)
	}
	if err := config.Validate(cfg); err != nil {
		return Request{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	paths, err := normalizePaths(input.ChangedPaths)
	if err != nil {
		return Request{}, err
	}
	include, err := roleSet(input.Include, "include")
	if err != nil {
		return Request{}, err
	}
	exclude, err := roleSet(input.Exclude, "exclude")
	if err != nil {
		return Request{}, err
	}
	for role := range include {
		if _, conflict := exclude[role]; conflict {
			return Request{}, fmt.Errorf("%w: role %q is both included and excluded", ErrInvalid, role)
		}
	}
	if _, excluded := exclude[config.RoleCodeReviewer]; excluded {
		return Request{}, fmt.Errorf("%w: code-reviewer is mandatory", ErrInvalid)
	}

	signals := stringSet(input.Signals)
	strong := stringSet(input.StrongSignals)
	protected := map[string][]string{
		config.RoleCodeReviewer: {"mandatory for implementation review"},
	}
	sources := map[string]string{config.RoleCodeReviewer: "mode"}

	for index, rule := range cfg.Routing {
		matchedPaths, matchedSignals, matched, err := matchRule(rule, paths, signals)
		if err != nil {
			return Request{}, fmt.Errorf("%w: routing rule %d: %v", ErrInvalid, index, err)
		}
		if !matched {
			continue
		}
		for _, role := range rule.AddRoles {
			if _, disabled := exclude[role]; disabled || !cfg.Roles[role].Enabled {
				continue
			}
			isStrong := len(matchedPaths) > 0
			for _, signal := range matchedSignals {
				if _, ok := strong[signal]; ok {
					isStrong = true
				}
			}
			if !isStrong {
				continue
			}
			reason := "matched routing rule"
			if len(matchedPaths) > 0 {
				reason += " for " + strings.Join(matchedPaths, ", ")
			}
			if len(matchedSignals) > 0 {
				reason += " with " + strings.Join(matchedSignals, ", ")
			}
			protected[role] = append(protected[role], reason)
			sources[role] = "routing"
		}
	}
	for role := range include {
		if !cfg.Roles[role].Enabled {
			return Request{}, fmt.Errorf("%w: included role %q is disabled", ErrInvalid, role)
		}
		protected[role] = append(protected[role], "explicitly included by user")
		sources[role] = "user"
	}
	if cfg.Roles[config.RoleSecurityAuditor].Enabled {
		if _, explicitlyExcluded := exclude[config.RoleSecurityAuditor]; !explicitlyExcluded {
			protected[config.RoleSecurityAuditor] = append(protected[config.RoleSecurityAuditor], "protected security boundary for untrusted changes")
			sources[config.RoleSecurityAuditor] = "security-policy"
		}
	}

	request := Request{
		Version:         Version,
		RunID:           input.RunID,
		Protected:       []Decision{},
		Candidates:      []Candidate{},
		Excluded:        []Decision{},
		EvidenceSources: append([]EvidenceSource(nil), input.Evidence...),
	}
	if len(request.EvidenceSources) == 0 {
		request.EvidenceSources = []EvidenceSource{{ID: "snapshot.diff", Kind: "diff", Source: "frozen snapshot diff"}}
	}
	for _, role := range config.KnownRoles() {
		switch {
		case !cfg.Roles[role].Enabled:
			request.Excluded = append(request.Excluded, Decision{Role: role, Source: "config", Reasons: []string{"disabled by configuration"}})
		case containsRole(exclude, role):
			request.Excluded = append(request.Excluded, Decision{Role: role, Source: "user", Reasons: []string{"explicitly excluded by user"}})
		case len(protected[role]) > 0:
			request.Protected = append(request.Protected, Decision{Role: role, Protected: true, Source: sources[role], Reasons: unique(protected[role])})
		default:
			request.Candidates = append(request.Candidates, Candidate{Role: role, Scope: roleScope(role)})
		}
	}
	sortRequest(&request)
	return request, nil
}

func Resolve(request Request, proposal protocol.SemanticRoutingEnvelope) (Result, error) {
	if proposal.Version != Version || proposal.RunID != request.RunID {
		return Result{}, fmt.Errorf("%w: semantic response identity mismatch", ErrInvalid)
	}
	expected := make(map[string]Candidate, len(request.Candidates))
	for _, candidate := range request.Candidates {
		expected[candidate.Role] = candidate
	}
	evidence := make(map[string]struct{}, len(request.EvidenceSources))
	for _, source := range request.EvidenceSources {
		evidence[source.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(proposal.Decisions))
	result := Result{Selected: append([]Decision(nil), request.Protected...), Excluded: append([]Decision(nil), request.Excluded...), Resolution: "semantic"}
	for _, decision := range proposal.Decisions {
		if _, ok := expected[decision.Role]; !ok {
			return Result{}, fmt.Errorf("%w: unexpected semantic role %q", ErrInvalid, decision.Role)
		}
		if _, duplicate := seen[decision.Role]; duplicate {
			return Result{}, fmt.Errorf("%w: duplicate semantic role %q", ErrInvalid, decision.Role)
		}
		if decision.Decision != "include" && decision.Decision != "exclude" {
			return Result{}, fmt.Errorf("%w: invalid decision for %q", ErrInvalid, decision.Role)
		}
		if strings.TrimSpace(decision.Reason) == "" || len(decision.EvidenceRefs) == 0 {
			return Result{}, fmt.Errorf("%w: incomplete decision for %q", ErrInvalid, decision.Role)
		}
		for _, reference := range decision.EvidenceRefs {
			if _, ok := evidence[reference]; !ok {
				return Result{}, fmt.Errorf("%w: role %q references unknown evidence %q", ErrInvalid, decision.Role, reference)
			}
		}
		seen[decision.Role] = struct{}{}
		resolved := Decision{Role: decision.Role, Source: "semantic", Reasons: []string{strings.TrimSpace(decision.Reason)}}
		if decision.Decision == "include" {
			result.Selected = append(result.Selected, resolved)
		} else {
			result.Excluded = append(result.Excluded, resolved)
		}
	}
	if len(seen) != len(expected) {
		return Result{}, fmt.Errorf("%w: semantic router decided %d of %d roles", ErrInvalid, len(seen), len(expected))
	}
	sortResult(&result)
	return result, nil
}

func Deterministic(request Request) Result {
	result := Result{Selected: append([]Decision(nil), request.Protected...), Excluded: append([]Decision(nil), request.Excluded...), Resolution: "deterministic"}
	sortResult(&result)
	return result
}

func Fallback(request Request, reason string) Result {
	result := Result{
		Selected:       append([]Decision(nil), request.Protected...),
		Excluded:       append([]Decision(nil), request.Excluded...),
		Resolution:     "fallback",
		Degraded:       true,
		FallbackReason: strings.TrimSpace(reason),
	}
	for _, candidate := range request.Candidates {
		result.Selected = append(result.Selected, Decision{Role: candidate.Role, Source: "fallback", Reasons: []string{"semantic routing unavailable; included conservatively"}})
	}
	sortResult(&result)
	return result
}

func DetectSignals(paths []string, diff string) (signals, strong []string) {
	lower := strings.ToLower(diff)
	checks := []struct {
		name  string
		terms []string
	}{
		{"react", []string{"from \"react\"", "from 'react'", "react."}},
		{"security", []string{"authorization", "authentication", "permission", "token", "password", "secret", "oauth"}},
		{"sql", []string{"select ", "insert ", "update ", "delete ", "transaction", "migration"}},
		{"contract", []string{"openapi", "protobuf", "json schema", "public dto"}},
		{"reliability", []string{"retry", "timeout", "backpressure", "circuit breaker", "idempot"}},
		{"messaging", []string{"kafka", "consumer", "producer", "queue", "acknowledg", "offset"}},
		{"storage", []string{"redis", "cache", "elasticsearch", "opensearch", "object storage"}},
		{"architecture", []string{"new module", "new service", "dependency direction"}},
		{"observable-behavior", []string{"response", "status code", "user-visible", "behavior"}},
		{"duplication", []string{"duplicate", "copy"}},
	}
	for _, check := range checks {
		for _, term := range check.terms {
			if strings.Contains(lower, term) {
				signals = append(signals, check.name)
				strong = append(strong, check.name)
				break
			}
		}
	}
	for _, path := range paths {
		lowerPath := strings.ToLower(path)
		switch {
		case strings.HasSuffix(lowerPath, "_test.go"), strings.Contains(lowerPath, "/tests/"), strings.Contains(lowerPath, ".test."), strings.Contains(lowerPath, ".spec."):
			signals = append(signals, "tests")
			strong = append(strong, "tests")
		case strings.HasSuffix(lowerPath, ".py"):
			signals = append(signals, "python")
		case strings.HasSuffix(lowerPath, ".ts"), strings.HasSuffix(lowerPath, ".tsx"):
			signals = append(signals, "typescript")
		}
	}
	return unique(signals), unique(strong)
}

func RelevantPaths(role string, paths []string) []string {
	patterns := rolePatterns[role]
	if len(patterns) == 0 {
		return append([]string(nil), paths...)
	}
	var result []string
	for _, path := range paths {
		for _, pattern := range patterns {
			matched, _ := doublestar.PathMatch(pattern, filepath.ToSlash(path))
			if matched {
				result = append(result, filepath.ToSlash(path))
				break
			}
		}
	}
	if len(result) == 0 {
		return append([]string(nil), paths...)
	}
	return unique(result)
}

var rolePatterns = map[string][]string{
	config.RoleGolangExpert:         {"**/*.go", "go.mod", "go.sum"},
	config.RolePythonExpert:         {"**/*.py", "**/pyproject.toml", "**/requirements*.txt", "**/poetry.lock", "**/uv.lock"},
	config.RoleTypeScriptExpert:     {"**/*.ts", "**/*.tsx", "**/package.json", "**/tsconfig*.json"},
	config.RoleReactExpert:          {"**/*.tsx", "**/*.jsx", "**/package.json"},
	config.RoleFrontendExpert:       {"**/*.ts", "**/*.tsx", "**/*.js", "**/*.jsx", "**/*.css", "**/*.scss", "**/*.less", "**/*.html"},
	config.RoleSQLExpert:            {"**/*.sql", "**/migrations/**"},
	config.RoleContractReviewer:     {"**/*.proto", "**/openapi/**", "**/brief/**", "**/*schema*.json"},
	config.RoleInfrastructureExpert: {"**/Dockerfile*", "**/k8s/**", "**/kubernetes/**", "**/helm/**", "**/charts/**", ".github/workflows/**", "**/teamcity/**"},
	config.RoleSkillAuthoringExpert: {"**/SKILL.md", "**/AGENTS.md", "**/skills/**", "**/templates/**"},
}

func matchRule(rule config.RoutingRule, paths []string, signals map[string]struct{}) ([]string, []string, bool, error) {
	var matchedPaths []string
	if len(rule.When.Paths) > 0 {
		for _, path := range paths {
			for _, pattern := range rule.When.Paths {
				matched, err := doublestar.PathMatch(filepath.ToSlash(pattern), path)
				if err != nil {
					return nil, nil, false, err
				}
				if matched {
					matchedPaths = append(matchedPaths, path)
					break
				}
			}
		}
		if len(matchedPaths) == 0 {
			return nil, nil, false, nil
		}
	}
	var matchedSignals []string
	if len(rule.When.Signals) > 0 {
		for _, signal := range rule.When.Signals {
			if _, ok := signals[signal]; ok {
				matchedSignals = append(matchedSignals, signal)
			}
		}
		if len(matchedSignals) == 0 {
			return nil, nil, false, nil
		}
	}
	return unique(matchedPaths), unique(matchedSignals), true, nil
}

func roleSet(values []string, field string) (map[string]struct{}, error) {
	known := stringSet(config.KnownRoles())
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := known[value]; !ok {
			return nil, fmt.Errorf("%w: %s contains unknown role %q", ErrInvalid, field, value)
		}
		if _, duplicate := result[value]; duplicate {
			return nil, fmt.Errorf("%w: %s contains duplicate role %q", ErrInvalid, field, value)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func normalizePaths(values []string) ([]string, error) {
	var result []string
	for _, value := range values {
		value = filepath.ToSlash(filepath.Clean(value))
		if value == "." || filepath.IsAbs(value) || value == ".." || strings.HasPrefix(value, "../") {
			return nil, fmt.Errorf("%w: changed path must be repository-relative: %q", ErrInvalid, value)
		}
		result = append(result, value)
	}
	return unique(result), nil
}

func containsRole(values map[string]struct{}, role string) bool { _, ok := values[role]; return ok }

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func unique(values []string) []string {
	set := stringSet(values)
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortRequest(request *Request) {
	sort.Slice(request.Protected, func(i, j int) bool { return order(request.Protected[i].Role) < order(request.Protected[j].Role) })
	sort.Slice(request.Candidates, func(i, j int) bool { return order(request.Candidates[i].Role) < order(request.Candidates[j].Role) })
	sort.Slice(request.Excluded, func(i, j int) bool { return order(request.Excluded[i].Role) < order(request.Excluded[j].Role) })
	sort.Slice(request.EvidenceSources, func(i, j int) bool { return request.EvidenceSources[i].ID < request.EvidenceSources[j].ID })
}

func sortResult(result *Result) {
	sort.SliceStable(result.Selected, func(i, j int) bool {
		if result.Selected[i].Protected != result.Selected[j].Protected {
			return result.Selected[i].Protected
		}
		return order(result.Selected[i].Role) < order(result.Selected[j].Role)
	})
	sort.Slice(result.Excluded, func(i, j int) bool { return order(result.Excluded[i].Role) < order(result.Excluded[j].Role) })
}

func order(role string) int {
	for index, known := range config.KnownRoles() {
		if known == role {
			return index
		}
	}
	return len(config.KnownRoles())
}

func roleScope(role string) string {
	scopes := map[string]string{
		config.RoleArchitectReviewer:    "architecture boundaries, dependency direction, rollout, compatibility and system failure modes",
		config.RoleGolangExpert:         "Go runtime semantics, errors, contexts, concurrency and resource lifetime",
		config.RolePythonExpert:         "Python runtime, typing, asyncio, cleanup and framework semantics",
		config.RoleTypeScriptExpert:     "TypeScript soundness, async behavior and runtime-schema drift",
		config.RoleReactExpert:          "React hooks, lifecycle, rendering and ecosystem semantics",
		config.RoleFrontendExpert:       "browser behavior, accessibility, navigation, security and UI states",
		config.RoleSkillAuthoringExpert: "skill triggering, instructions, tool contracts, references and workflow safety",
		config.RoleReliabilityExpert:    "timeouts, retries, idempotency, backpressure and graceful degradation",
		config.RoleMessagingExpert:      "delivery, ordering, acknowledgements, retries, DLQ and transactional messaging",
		config.RoleInfrastructureExpert: "deployment configuration, probes, resources, rollout and CI/CD safety",
		config.RoleStorageExpert:        "cache, search, object storage consistency, invalidation and lifecycle",
		config.RoleSQLExpert:            "SQL correctness, transactions, locking, indexes and migration safety",
		config.RoleContractReviewer:     "public schemas, compatibility, optionality and producer-consumer contracts",
		config.RoleQAExpert:             "specific changed behavior, negative paths, boundaries and ineffective tests",
		config.RoleCodeSimplifier:       "demonstrable maintenance risk in changed code only",
	}
	return scopes[role]
}
