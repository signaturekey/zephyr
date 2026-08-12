package config

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	ProcessProbe          = "probe"
	ProcessSemanticRouter = "semantic-router"
	ProcessEvidenceGate   = "evidence-gate"
	modelPolicyHeader     = "zephyr-codex-model-policy-v1"
)

type ModelSettings struct {
	Model   string `json:"model" yaml:"model"`
	Effort  string `json:"effort" yaml:"effort"`
	Fast    bool   `json:"fast" yaml:"fast"`
	fastSet bool
}

type ModelPolicy struct {
	Default ModelSettings     `json:"default" yaml:"default"`
	Stages  ModelPolicyStages `json:"stages" yaml:"stages"`
}

type ModelPolicyStages struct {
	Probe          ModelSettings       `json:"probe" yaml:"probe"`
	SemanticRouter ModelSettings       `json:"semantic_router" yaml:"semantic_router"`
	Reviewers      ReviewerModelPolicy `json:"reviewers" yaml:"reviewers"`
	EvidenceGate   ModelSettings       `json:"evidence_gate" yaml:"evidence_gate"`
}

type ReviewerModelPolicy struct {
	Default ModelSettings            `json:"default" yaml:"default"`
	Roles   map[string]ModelSettings `json:"roles" yaml:"roles"`
}

type ResolvedModelPolicy struct {
	entries map[string]ModelSettings
}

func (policy ResolvedModelPolicy) Entry(process string) (ModelSettings, bool) {
	value, ok := policy.entries[process]
	return value, ok
}

func (policy ResolvedModelPolicy) MarshalText() ([]byte, error) {
	if len(policy.entries) != len(KnownRoles())+3 {
		return nil, errors.New("resolved model policy has an incomplete process set")
	}
	var output bytes.Buffer
	output.WriteString(modelPolicyHeader)
	output.WriteByte('\n')
	for _, process := range modelPolicyProcessOrder() {
		settings, ok := policy.Entry(process)
		if !ok {
			return nil, fmt.Errorf("resolved model policy is missing %q", process)
		}
		if err := validateEffectiveModelSettings(process, settings); err != nil {
			return nil, err
		}
		role := "-"
		if strings.HasPrefix(process, "reviewer:") {
			role = strings.TrimPrefix(process, "reviewer:")
		}
		fmt.Fprintf(&output, "%s\t%s\t%s\t%s\t%t\n", processName(process), role, settings.Model, settings.Effort, settings.Fast)
	}
	return output.Bytes(), nil
}

func ResolveModelPolicy(cfg Config) (ResolvedModelPolicy, error) {
	if err := validateModelPolicy(cfg.ModelPolicy); err != nil {
		return ResolvedModelPolicy{}, err
	}
	entries := make(map[string]ModelSettings, len(KnownRoles())+3)
	entries[ProcessProbe] = mergeModelSettings(cfg.ModelPolicy.Default, cfg.ModelPolicy.Stages.Probe)
	entries[ProcessSemanticRouter] = mergeModelSettings(cfg.ModelPolicy.Default, cfg.ModelPolicy.Stages.SemanticRouter)
	reviewerDefault := mergeModelSettings(cfg.ModelPolicy.Default, cfg.ModelPolicy.Stages.Reviewers.Default)
	for _, role := range KnownRoles() {
		entries[reviewerProcess(role)] = mergeModelSettings(reviewerDefault, cfg.ModelPolicy.Stages.Reviewers.Roles[role])
	}
	entries[ProcessEvidenceGate] = mergeModelSettings(cfg.ModelPolicy.Default, cfg.ModelPolicy.Stages.EvidenceGate)

	for process, settings := range entries {
		if err := validateEffectiveModelSettings(process, settings); err != nil {
			return ResolvedModelPolicy{}, err
		}
	}
	return ResolvedModelPolicy{entries: entries}, nil
}

func reviewerProcess(role string) string { return "reviewer:" + role }

func processName(process string) string {
	if strings.HasPrefix(process, "reviewer:") {
		return "reviewer"
	}
	return process
}

func modelPolicyProcessOrder() []string {
	roles := append([]string(nil), KnownRoles()...)
	sort.Strings(roles)
	processes := make([]string, 0, len(roles)+3)
	processes = append(processes, ProcessProbe, ProcessSemanticRouter)
	for _, role := range roles {
		processes = append(processes, reviewerProcess(role))
	}
	return append(processes, ProcessEvidenceGate)
}

func mergeModelSettings(base, overlay ModelSettings) ModelSettings {
	result := base
	if overlay.Model != "" {
		result.Model = overlay.Model
	}
	if overlay.Effort != "" {
		result.Effort = overlay.Effort
	}
	if overlay.fastSet {
		result.Fast = overlay.Fast
	}
	return result
}

func validateModelPolicy(policy ModelPolicy) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	for role := range policy.Stages.Reviewers.Roles {
		if _, ok := knownRoleSet()[role]; !ok {
			return invalid("model_policy.stages.reviewers.roles contains unknown reviewer role %q", role)
		}
	}
	for _, item := range []struct {
		path     string
		settings ModelSettings
	}{
		{"model_policy.default", policy.Default},
		{"model_policy.stages.probe", policy.Stages.Probe},
		{"model_policy.stages.semantic_router", policy.Stages.SemanticRouter},
		{"model_policy.stages.reviewers.default", policy.Stages.Reviewers.Default},
		{"model_policy.stages.evidence_gate", policy.Stages.EvidenceGate},
	} {
		if err := validateConfiguredModelSettings(item.path, item.settings); err != nil {
			return err
		}
	}
	for role, settings := range policy.Stages.Reviewers.Roles {
		if err := validateConfiguredModelSettings("model_policy.stages.reviewers.roles."+role, settings); err != nil {
			return err
		}
	}
	return nil
}

func validateConfiguredModelSettings(path string, settings ModelSettings) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	if settings.Model != "" && settings.Model != "inherit" && !modelPattern.MatchString(settings.Model) {
		return invalid("%s.model must be inherit or a safe model identifier, got %q", path, settings.Model)
	}
	if settings.Effort != "" {
		if _, ok := knownReasoningEfforts[settings.Effort]; !ok {
			return invalid("%s.effort must be one of none, low, medium, high, xhigh, or max, got %q", path, settings.Effort)
		}
	}
	return nil
}

func validateEffectiveModelSettings(process string, settings ModelSettings) error {
	if settings.Model == "" || settings.Effort == "" {
		return fmt.Errorf("%w: resolved model policy for %s must contain model and effort", ErrInvalid, process)
	}
	return validateConfiguredModelSettings("resolved model policy for "+process, settings)
}

var (
	modelPattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	knownReasoningEfforts = map[string]struct{}{"none": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {}}
)
