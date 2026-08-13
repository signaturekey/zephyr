package routing

import (
	"errors"
	"fmt"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/signaturekey/zephyr/internal/config"
)

type Mode string

const (
	ModePlan           Mode = "plan"
	ModeImplementation Mode = "implementation"
	ModeAlignment      Mode = "alignment"
	ModeAuto           Mode = "auto"
)

const (
	ReasonRequiredByMode    = "required-by-mode"
	ReasonForcedInclusion   = "forced-inclusion"
	ReasonRoutingRule       = "routing-rule"
	ReasonDisabled          = "disabled"
	ReasonExplicitExclusion = "explicitly-excluded"
	ReasonNoSignal          = "no-matching-signal"
	ReasonProfileLimit      = "profile-limit"
	ReasonSemanticCandidate = "semantic-candidate"
	ReasonSemanticIncluded  = "semantic-include"
	ReasonSemanticExcluded  = "semantic-exclude"
	ReasonSemanticFallback  = "semantic-fallback"
	ReasonSecurityBoundary  = "security-boundary"
)

var ErrInvalidInput = errors.New("invalid routing input")

type Input struct {
	Mode          Mode     `json:"mode"`
	ChangedPaths  []string `json:"changed_paths"`
	Signals       []string `json:"signals"`
	StrongSignals []string `json:"strong_signals,omitempty"`
	HasPlan       bool     `json:"has_plan"`
	HasChanges    bool     `json:"has_changes"`
	ForceInclude  []string `json:"force_include,omitempty"`
	ForceExclude  []string `json:"force_exclude,omitempty"`
}

type Result struct {
	Mode           Mode           `json:"mode"`
	Profile        config.Profile `json:"profile"`
	Limit          int            `json:"limit"`
	Resolution     string         `json:"resolution,omitempty"`
	Degraded       bool           `json:"degraded,omitempty"`
	FallbackReason string         `json:"fallback_reason,omitempty"`
	Selected       []Decision     `json:"selected"`
	Excluded       []Decision     `json:"excluded"`
}

type Decision struct {
	Role      string   `json:"role"`
	Protected bool     `json:"protected"`
	Source    string   `json:"source,omitempty"`
	Reasons   []Reason `json:"reasons"`
}

type Reason struct {
	Code           string   `json:"code"`
	Detail         string   `json:"detail"`
	RuleIndex      *int     `json:"rule_index,omitempty"`
	MatchedPaths   []string `json:"matched_paths,omitempty"`
	MatchedSignals []string `json:"matched_signals,omitempty"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
	Confidence     *float64 `json:"confidence,omitempty"`
}

type roleState struct {
	role                string
	required            bool
	forced              bool
	candidate           bool
	excluded            bool
	matchedPath         bool
	matchedStrongSignal bool
	reasons             []Reason
}

func Route(cfg config.Config, input Input) (Result, error) {
	mode, limit, states, err := classifyRoles(cfg, input)
	if err != nil {
		return Result{}, err
	}

	candidates := make([]*roleState, 0, len(states))
	forcedCount := 0
	for _, state := range states {
		if state.candidate && !state.excluded {
			candidates = append(candidates, state)
			if state.required || state.forced {
				forcedCount++
			}
		}
	}
	if forcedCount > limit {
		return Result{}, fmt.Errorf("%w: %d mandatory/force-included roles exceed %s profile limit %d", ErrInvalidInput, forcedCount, cfg.Profile, limit)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.required != right.required {
			return left.required
		}
		if left.forced != right.forced {
			return left.forced
		}
		return rolePriority(left.role) < rolePriority(right.role)
	})
	selectedSet := make(map[string]struct{}, min(limit, len(candidates)))
	for i, state := range candidates {
		if i < limit {
			selectedSet[state.role] = struct{}{}
			continue
		}
		state.excluded = true
		state.reasons = append(state.reasons, Reason{
			Code:   ReasonProfileLimit,
			Detail: fmt.Sprintf("role was displaced by higher-priority roles at the %s profile limit of %d", cfg.Profile, limit),
		})
	}

	result := Result{Mode: mode, Profile: cfg.Profile, Limit: limit}
	for _, state := range candidates {
		if _, selected := selectedSet[state.role]; selected {
			result.Selected = append(result.Selected, Decision{Role: state.role, Reasons: cloneReasons(state.reasons)})
		}
	}

	excluded := make([]*roleState, 0, len(states)-len(result.Selected))
	for _, state := range states {
		if _, selected := selectedSet[state.role]; selected {
			continue
		}
		if !state.excluded {
			state.reasons = append(state.reasons, Reason{Code: ReasonNoSignal, Detail: "no mandatory, explicit, path, or semantic signal selected this role"})
		}
		excluded = append(excluded, state)
	}
	sort.Slice(excluded, func(i, j int) bool {
		return rolePriority(excluded[i].role) < rolePriority(excluded[j].role)
	})
	for _, state := range excluded {
		result.Excluded = append(result.Excluded, Decision{Role: state.role, Reasons: cloneReasons(state.reasons)})
	}

	return result, nil
}

func classifyRoles(cfg config.Config, input Input) (Mode, int, map[string]*roleState, error) {
	if err := config.Validate(cfg); err != nil {
		return "", 0, nil, fmt.Errorf("route with configuration: %w", err)
	}

	mode, err := resolveMode(input)
	if err != nil {
		return "", 0, nil, err
	}
	limit := cfg.Limits.MaxRolesStandard
	if cfg.Profile == config.ProfileThorough {
		limit = cfg.Limits.MaxRolesThorough
	}

	include, err := roleSet(input.ForceInclude, "force_include")
	if err != nil {
		return "", 0, nil, err
	}
	exclude, err := roleSet(input.ForceExclude, "force_exclude")
	if err != nil {
		return "", 0, nil, err
	}
	for role := range include {
		if _, conflict := exclude[role]; conflict {
			return "", 0, nil, fmt.Errorf("%w: role %q is both force-included and force-excluded", ErrInvalidInput, role)
		}
	}

	states := make(map[string]*roleState, len(config.KnownRoles()))
	for _, role := range config.KnownRoles() {
		states[role] = &roleState{role: role}
	}

	requiredRole := requiredRoleForMode(mode)
	if requiredRole != "" {
		state := states[requiredRole]
		if !cfg.Roles[requiredRole].Enabled {
			return "", 0, nil, fmt.Errorf("%w: mode %q requires disabled role %q", ErrInvalidInput, mode, requiredRole)
		}
		if _, explicitlyExcluded := exclude[requiredRole]; explicitlyExcluded {
			return "", 0, nil, fmt.Errorf("%w: mode %q requires force-excluded role %q", ErrInvalidInput, mode, requiredRole)
		}
		state.required = true
		state.candidate = true
		state.reasons = append(state.reasons, Reason{
			Code:   ReasonRequiredByMode,
			Detail: fmt.Sprintf("%s is mandatory for %s reviews", requiredRole, mode),
		})
	}

	paths, err := normalizePaths(input.ChangedPaths)
	if err != nil {
		return "", 0, nil, err
	}
	signals := normalizedSignals(input.Signals)
	strongSignals := normalizedSignals(input.StrongSignals)
	for i, rule := range cfg.Routing {
		matchedPaths, matchedSignals, matched, err := matchRule(rule, paths, signals)
		if err != nil {
			return "", 0, nil, fmt.Errorf("match routing rule %d: %w", i, err)
		}
		if !matched {
			continue
		}
		for _, role := range rule.AddRoles {
			state := states[role]
			state.candidate = true
			state.matchedPath = state.matchedPath || len(matchedPaths) > 0
			state.matchedStrongSignal = state.matchedStrongSignal || intersects(matchedSignals, strongSignals)
			ruleIndex := i
			state.reasons = append(state.reasons, Reason{
				Code:           ReasonRoutingRule,
				Detail:         routingDetail(matchedPaths, matchedSignals),
				RuleIndex:      &ruleIndex,
				MatchedPaths:   matchedPaths,
				MatchedSignals: matchedSignals,
			})
		}
	}

	for role := range include {
		if !cfg.Roles[role].Enabled {
			return "", 0, nil, fmt.Errorf("%w: cannot force-include disabled role %q", ErrInvalidInput, role)
		}
		state := states[role]
		state.candidate = true
		state.forced = true
		state.reasons = append(state.reasons, Reason{
			Code:   ReasonForcedInclusion,
			Detail: "role explicitly included by the user",
		})
	}

	for _, role := range config.KnownRoles() {
		state := states[role]
		if !cfg.Roles[role].Enabled {
			state.excluded = true
			state.reasons = append(state.reasons, Reason{Code: ReasonDisabled, Detail: "role is disabled by configuration"})
			continue
		}
		if _, explicitlyExcluded := exclude[role]; explicitlyExcluded {
			state.candidate = false
			state.excluded = true
			state.reasons = append(state.reasons, Reason{Code: ReasonExplicitExclusion, Detail: "role explicitly excluded by the user"})
		}
	}

	return mode, limit, states, nil
}

func resolveMode(input Input) (Mode, error) {
	if input.Mode != ModeAuto {
		switch input.Mode {
		case ModePlan, ModeImplementation, ModeAlignment:
			return input.Mode, nil
		default:
			return "", fmt.Errorf("%w: unsupported mode %q", ErrInvalidInput, input.Mode)
		}
	}

	hasChanges := input.HasChanges || len(input.ChangedPaths) > 0
	switch {
	case input.HasPlan && hasChanges:
		return ModeAlignment, nil
	case input.HasPlan:
		return ModePlan, nil
	case hasChanges:
		return ModeImplementation, nil
	default:
		return "", fmt.Errorf("%w: auto mode needs a plan, changes, or both", ErrInvalidInput)
	}
}

func requiredRoleForMode(mode Mode) string {
	switch mode {
	case ModePlan:
		return config.RoleArchitectReviewer
	case ModeImplementation, ModeAlignment:
		return config.RoleCodeReviewer
	default:
		return ""
	}
}

func roleSet(roles []string, field string) (map[string]struct{}, error) {
	known := make(map[string]struct{}, len(config.KnownRoles()))
	for _, role := range config.KnownRoles() {
		known[role] = struct{}{}
	}
	result := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if _, ok := known[role]; !ok {
			return nil, fmt.Errorf("%w: %s contains unknown role %q", ErrInvalidInput, field, role)
		}
		if _, duplicate := result[role]; duplicate {
			return nil, fmt.Errorf("%w: %s contains duplicate role %q", ErrInvalidInput, field, role)
		}
		result[role] = struct{}{}
	}
	return result, nil
}

func normalizePaths(paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, value := range paths {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%w: changed_paths contains an empty path", ErrInvalidInput)
		}
		if strings.IndexByte(value, 0) >= 0 {
			return nil, fmt.Errorf("%w: changed path contains a NUL byte", ErrInvalidInput)
		}
		path := pathpkg.Clean(filepath.ToSlash(value))
		if path == "." || pathpkg.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
			return nil, fmt.Errorf("%w: changed path must be repository-relative: %q", ErrInvalidInput, value)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

func normalizedSignals(signals []string) map[string]struct{} {
	result := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		signal = strings.TrimSpace(signal)
		if signal != "" {
			result[signal] = struct{}{}
		}
	}
	return result
}

func matchRule(rule config.RoutingRule, paths []string, signals map[string]struct{}) ([]string, []string, bool, error) {
	var matchedPaths []string
	if len(rule.When.Paths) > 0 {
		for _, path := range paths {
			for _, pattern := range rule.When.Paths {
				matched, err := doublestar.Match(pattern, path)
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
	sort.Strings(matchedSignals)
	return matchedPaths, matchedSignals, true, nil
}

func routingDetail(paths, signals []string) string {
	parts := make([]string, 0, 2)
	if len(paths) > 0 {
		parts = append(parts, "matched paths: "+strings.Join(paths, ", "))
	}
	if len(signals) > 0 {
		parts = append(parts, "matched signals: "+strings.Join(signals, ", "))
	}
	return strings.Join(parts, "; ")
}

func rolePriority(role string) int {
	switch role {
	case config.RoleCodeReviewer:
		return 0
	case config.RoleSecurityAuditor:
		return 10
	case config.RoleReliabilityExpert:
		return 15
	case config.RoleSQLExpert:
		return 20
	case config.RoleStorageExpert:
		return 22
	case config.RoleMessagingExpert:
		return 25
	case config.RoleContractReviewer:
		return 30
	case config.RoleInfrastructureExpert:
		return 35
	case config.RoleGolangExpert:
		return 40
	case config.RolePythonExpert:
		return 41
	case config.RoleTypeScriptExpert:
		return 42
	case config.RoleReactExpert:
		return 43
	case config.RoleFrontendExpert:
		return 44
	case config.RoleSkillAuthoringExpert:
		return 46
	case config.RoleArchitectReviewer:
		return 50
	case config.RoleQAExpert:
		return 60
	case config.RoleCodeSimplifier:
		return 70
	default:
		return 1000
	}
}

func cloneReasons(reasons []Reason) []Reason {
	result := make([]Reason, len(reasons))
	for i, reason := range reasons {
		result[i] = reason
		result[i].MatchedPaths = append([]string(nil), reason.MatchedPaths...)
		result[i].MatchedSignals = append([]string(nil), reason.MatchedSignals...)
		result[i].EvidenceRefs = append([]string(nil), reason.EvidenceRefs...)
		if reason.Confidence != nil {
			confidence := *reason.Confidence
			result[i].Confidence = &confidence
		}
	}
	return result
}
