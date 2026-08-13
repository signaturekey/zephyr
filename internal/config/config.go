package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	configassets "github.com/signaturekey/zephyr/configs"
	"go.yaml.in/yaml/v3"
)

const (
	CurrentVersion = 1

	RoleCodeReviewer         = "code-reviewer"
	RoleArchitectReviewer    = "architect-reviewer"
	RoleGolangExpert         = "golang-expert"
	RolePythonExpert         = "python-expert"
	RoleTypeScriptExpert     = "typescript-expert"
	RoleFrontendExpert       = "frontend-expert"
	RoleReactExpert          = "react-expert"
	RoleSkillAuthoringExpert = "skill-authoring-expert"
	RoleReliabilityExpert    = "reliability-expert"
	RoleMessagingExpert      = "messaging-expert"
	RoleInfrastructureExpert = "infrastructure-expert"
	RoleStorageExpert        = "storage-expert"
	RoleSecurityAuditor      = "security-auditor"
	RoleSQLExpert            = "sql-expert"
	RoleContractReviewer     = "contract-reviewer"
	RoleQAExpert             = "qa-expert"
	RoleCodeSimplifier       = "code-simplifier"
)

var ErrInvalid = errors.New("invalid zephyr configuration")

type Profile string

const (
	ProfileStandard Profile = "standard"
	ProfileThorough Profile = "thorough"
)

type Config struct {
	Version         int                   `json:"version" yaml:"version"`
	Profile         Profile               `json:"profile" yaml:"profile"`
	Language        string                `json:"language" yaml:"language"`
	Limits          Limits                `json:"limits" yaml:"limits"`
	Roles           map[string]RoleConfig `json:"roles" yaml:"roles"`
	Routing         []RoutingRule         `json:"routing" yaml:"routing"`
	ModelPolicy     ModelPolicy           `json:"model_policy" yaml:"model_policy"`
	RestrictedPaths []string              `json:"restricted_paths" yaml:"restricted_paths"`
	Redaction       Redaction             `json:"redaction" yaml:"redaction"`
}

type Limits struct {
	MaxParallelReviewers int `json:"max_parallel_reviewers" yaml:"max_parallel_reviewers"`
	MaxRolesStandard     int `json:"max_roles_standard" yaml:"max_roles_standard"`
	MaxRolesThorough     int `json:"max_roles_thorough" yaml:"max_roles_thorough"`
	MaxFinalFindings     int `json:"max_final_findings" yaml:"max_final_findings"`
}

type RoleConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

type RoutingRule struct {
	When     RoutingCondition `json:"when" yaml:"when"`
	AddRoles []string         `json:"add_roles" yaml:"add_roles"`
}

type RoutingCondition struct {
	Paths   []string `json:"paths,omitempty" yaml:"paths,omitempty"`
	Signals []string `json:"signals,omitempty" yaml:"signals,omitempty"`
}

type Redaction struct {
	Enabled      bool     `json:"enabled" yaml:"enabled"`
	DenyPatterns []string `json:"deny_patterns" yaml:"deny_patterns"`
}

func KnownRoles() []string {
	return []string{
		RoleCodeReviewer,
		RoleArchitectReviewer,
		RoleGolangExpert,
		RolePythonExpert,
		RoleTypeScriptExpert,
		RoleReactExpert,
		RoleFrontendExpert,
		RoleSkillAuthoringExpert,
		RoleReliabilityExpert,
		RoleMessagingExpert,
		RoleInfrastructureExpert,
		RoleStorageExpert,
		RoleSecurityAuditor,
		RoleSQLExpert,
		RoleContractReviewer,
		RoleQAExpert,
		RoleCodeSimplifier,
	}
}

func Load(projectPath string) (Config, error) {
	var project []byte
	if projectPath != "" {
		resolved, err := resolveProjectPath(projectPath)
		if err != nil {
			return Config{}, err
		}

		project, err = os.ReadFile(resolved)
		if err != nil {
			return Config{}, fmt.Errorf("read project config %q: %w", resolved, err)
		}
	}

	return LoadBytes(project)
}

func LoadBytes(project []byte) (Config, error) {
	defaultYAML, err := configassets.ReadDefault()
	if err != nil {
		return Config{}, fmt.Errorf("read embedded defaults: %w", err)
	}
	defaults, err := decodePartial(defaultYAML, "embedded defaults")
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	apply(&cfg, defaults)
	if len(bytes.TrimSpace(project)) != 0 {
		overlay, err := decodePartial(project, "project config")
		if err != nil {
			return Config{}, err
		}
		apply(&cfg, overlay)
	}

	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	normalize(&cfg)
	return cfg, nil
}

func Validate(cfg Config) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}

	if cfg.Version != CurrentVersion {
		return invalid("version must be %d, got %d", CurrentVersion, cfg.Version)
	}
	if cfg.Profile != ProfileStandard && cfg.Profile != ProfileThorough {
		return invalid("profile must be %q or %q, got %q", ProfileStandard, ProfileThorough, cfg.Profile)
	}
	switch cfg.Language {
	case "auto", "go", "python", "typescript", "markdown":
	default:
		return invalid("language must be one of auto, go, python, typescript, or markdown, got %q", cfg.Language)
	}
	if cfg.Limits.MaxParallelReviewers <= 0 {
		return invalid("limits.max_parallel_reviewers must be positive")
	}
	if cfg.Limits.MaxRolesStandard <= 0 {
		return invalid("limits.max_roles_standard must be positive")
	}
	if cfg.Limits.MaxRolesThorough <= 0 {
		return invalid("limits.max_roles_thorough must be positive")
	}
	if cfg.Limits.MaxRolesThorough < cfg.Limits.MaxRolesStandard {
		return invalid("limits.max_roles_thorough must be at least max_roles_standard")
	}
	if cfg.Limits.MaxParallelReviewers > cfg.Limits.MaxRolesThorough {
		return invalid("limits.max_parallel_reviewers cannot exceed max_roles_thorough")
	}
	if cfg.Limits.MaxFinalFindings <= 0 {
		return invalid("limits.max_final_findings must be positive")
	}

	knownRoles := knownRoleSet()
	for _, role := range KnownRoles() {
		if _, ok := cfg.Roles[role]; !ok {
			return invalid("roles.%s is missing", role)
		}
	}
	for role := range cfg.Roles {
		if _, ok := knownRoles[role]; !ok {
			return invalid("roles contains unknown role %q", role)
		}
	}

	for i, rule := range cfg.Routing {
		if len(rule.When.Paths) == 0 && len(rule.When.Signals) == 0 {
			return invalid("routing[%d].when must contain paths or signals", i)
		}
		if len(rule.AddRoles) == 0 {
			return invalid("routing[%d].add_roles must not be empty", i)
		}
		for j, pattern := range rule.When.Paths {
			if err := validatePattern(pattern); err != nil {
				return invalid("routing[%d].when.paths[%d]: %v", i, j, err)
			}
		}
		for j, signal := range rule.When.Signals {
			if !slugPattern.MatchString(signal) {
				return invalid("routing[%d].when.signals[%d] is not a kebab-case signal: %q", i, j, signal)
			}
		}
		seenRoles := make(map[string]struct{}, len(rule.AddRoles))
		for j, role := range rule.AddRoles {
			if _, ok := knownRoles[role]; !ok {
				return invalid("routing[%d].add_roles[%d] contains unknown role %q", i, j, role)
			}
			if _, duplicate := seenRoles[role]; duplicate {
				return invalid("routing[%d].add_roles contains duplicate role %q", i, role)
			}
			seenRoles[role] = struct{}{}
		}
	}

	for i, pattern := range cfg.RestrictedPaths {
		if err := validatePattern(pattern); err != nil {
			return invalid("restricted_paths[%d]: %v", i, err)
		}
	}
	for i, pattern := range cfg.Redaction.DenyPatterns {
		if err := validatePattern(pattern); err != nil {
			return invalid("redaction.deny_patterns[%d]: %v", i, err)
		}
	}
	if _, err := ResolveModelPolicy(cfg); err != nil {
		return err
	}

	return nil
}

type partialConfig struct {
	Version         *int                         `yaml:"version"`
	Profile         *Profile                     `yaml:"profile"`
	Language        *string                      `yaml:"language"`
	Limits          *partialLimits               `yaml:"limits"`
	Roles           map[string]partialRoleConfig `yaml:"roles"`
	Routing         *[]RoutingRule               `yaml:"routing"`
	ModelPolicy     *partialModelPolicy          `yaml:"model_policy"`
	RestrictedPaths *[]string                    `yaml:"restricted_paths"`
	Redaction       *partialRedaction            `yaml:"redaction"`
}

type partialLimits struct {
	MaxParallelReviewers *int `yaml:"max_parallel_reviewers"`
	MaxRolesStandard     *int `yaml:"max_roles_standard"`
	MaxRolesThorough     *int `yaml:"max_roles_thorough"`
	MaxFinalFindings     *int `yaml:"max_final_findings"`
}

type partialRoleConfig struct {
	Enabled *bool `yaml:"enabled"`
}

type partialRedaction struct {
	Enabled      *bool     `yaml:"enabled"`
	DenyPatterns *[]string `yaml:"deny_patterns"`
}

type partialModelSettings struct {
	Model  *string `yaml:"model"`
	Effort *string `yaml:"effort"`
	Fast   *bool   `yaml:"fast"`
}

type partialModelPolicy struct {
	Default *partialModelSettings     `yaml:"default"`
	Stages  *partialModelPolicyStages `yaml:"stages"`
}

type partialModelPolicyStages struct {
	Probe          *partialModelSettings       `yaml:"probe"`
	SemanticRouter *partialModelSettings       `yaml:"semantic_router"`
	Reviewers      *partialReviewerModelPolicy `yaml:"reviewers"`
	EvidenceGate   *partialModelSettings       `yaml:"evidence_gate"`
}

type partialReviewerModelPolicy struct {
	Default *partialModelSettings           `yaml:"default"`
	Roles   map[string]partialModelSettings `yaml:"roles"`
}

func decodePartial(data []byte, source string) (partialConfig, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	var cfg partialConfig
	if err := decoder.Decode(&cfg); err != nil {
		return partialConfig{}, fmt.Errorf("%w: decode %s: %v", ErrInvalid, source, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return partialConfig{}, fmt.Errorf("%w: %s contains multiple YAML documents", ErrInvalid, source)
		}
		return partialConfig{}, fmt.Errorf("%w: decode trailing %s data: %v", ErrInvalid, source, err)
	}
	return cfg, nil
}

func apply(dst *Config, src partialConfig) {
	if src.Version != nil {
		dst.Version = *src.Version
	}
	if src.Profile != nil {
		dst.Profile = *src.Profile
	}
	if src.Language != nil {
		dst.Language = *src.Language
	}
	if src.Limits != nil {
		if src.Limits.MaxParallelReviewers != nil {
			dst.Limits.MaxParallelReviewers = *src.Limits.MaxParallelReviewers
		}
		if src.Limits.MaxRolesStandard != nil {
			dst.Limits.MaxRolesStandard = *src.Limits.MaxRolesStandard
		}
		if src.Limits.MaxRolesThorough != nil {
			dst.Limits.MaxRolesThorough = *src.Limits.MaxRolesThorough
		}
		if src.Limits.MaxFinalFindings != nil {
			dst.Limits.MaxFinalFindings = *src.Limits.MaxFinalFindings
		}
	}
	if src.Roles != nil {
		if dst.Roles == nil {
			dst.Roles = make(map[string]RoleConfig, len(src.Roles))
		}
		for name, overlay := range src.Roles {
			role := dst.Roles[name]
			if overlay.Enabled != nil {
				role.Enabled = *overlay.Enabled
			}
			dst.Roles[name] = role
		}
	}
	if src.Routing != nil {
		dst.Routing = append(dst.Routing, (*src.Routing)...)
	}
	if src.ModelPolicy != nil {
		applyModelPolicy(&dst.ModelPolicy, *src.ModelPolicy)
	}
	if src.RestrictedPaths != nil {
		dst.RestrictedPaths = append(dst.RestrictedPaths, (*src.RestrictedPaths)...)
	}
	if src.Redaction != nil {
		if src.Redaction.Enabled != nil {
			dst.Redaction.Enabled = *src.Redaction.Enabled
		}
		if src.Redaction.DenyPatterns != nil {
			dst.Redaction.DenyPatterns = append(dst.Redaction.DenyPatterns, (*src.Redaction.DenyPatterns)...)
		}
	}
}

func applyModelSettings(dst *ModelSettings, src partialModelSettings) {
	if src.Model != nil {
		dst.Model = *src.Model
	}
	if src.Effort != nil {
		dst.Effort = *src.Effort
	}
	if src.Fast != nil {
		dst.Fast = *src.Fast
		dst.fastSet = true
	}
}

func applyModelPolicy(dst *ModelPolicy, src partialModelPolicy) {
	if src.Default != nil {
		applyModelSettings(&dst.Default, *src.Default)
	}
	if src.Stages == nil {
		return
	}
	if src.Stages.Probe != nil {
		applyModelSettings(&dst.Stages.Probe, *src.Stages.Probe)
	}
	if src.Stages.SemanticRouter != nil {
		applyModelSettings(&dst.Stages.SemanticRouter, *src.Stages.SemanticRouter)
	}
	if src.Stages.EvidenceGate != nil {
		applyModelSettings(&dst.Stages.EvidenceGate, *src.Stages.EvidenceGate)
	}
	if src.Stages.Reviewers == nil {
		return
	}
	if src.Stages.Reviewers.Default != nil {
		applyModelSettings(&dst.Stages.Reviewers.Default, *src.Stages.Reviewers.Default)
	}
	if src.Stages.Reviewers.Roles != nil {
		if dst.Stages.Reviewers.Roles == nil {
			dst.Stages.Reviewers.Roles = make(map[string]ModelSettings, len(src.Stages.Reviewers.Roles))
		}
		for role, overlay := range src.Stages.Reviewers.Roles {
			settings := dst.Stages.Reviewers.Roles[role]
			applyModelSettings(&settings, overlay)
			dst.Stages.Reviewers.Roles[role] = settings
		}
	}
}

func normalize(cfg *Config) {
	for i := range cfg.Routing {
		cfg.Routing[i].When.Paths = uniqueStrings(cfg.Routing[i].When.Paths)
		cfg.Routing[i].When.Signals = uniqueStrings(cfg.Routing[i].When.Signals)
		cfg.Routing[i].AddRoles = uniqueStrings(cfg.Routing[i].AddRoles)
	}
	cfg.RestrictedPaths = uniqueStrings(cfg.RestrictedPaths)
	cfg.Redaction.DenyPatterns = uniqueStrings(cfg.Redaction.DenyPatterns)
}

func resolveProjectPath(projectPath string) (string, error) {
	info, err := os.Stat(projectPath)
	if err != nil {
		return "", fmt.Errorf("inspect project config path %q: %w", projectPath, err)
	}
	if info.IsDir() {
		return filepath.Join(projectPath, ".zephyr", "config.yaml"), nil
	}
	return projectPath, nil
}

func validatePattern(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return errors.New("glob pattern must not be empty")
	}
	if _, err := doublestar.Match(pattern, "validation/path"); err != nil {
		return fmt.Errorf("invalid glob %q: %w", pattern, err)
	}
	return nil
}

func knownRoleSet() map[string]struct{} {
	roles := KnownRoles()
	result := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		result[role] = struct{}{}
	}
	return result
}

func uniqueStrings(values []string) []string {
	if values == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func SortedRoleNames(cfg Config) []string {
	roles := make([]string, 0, len(cfg.Roles))
	for role := range cfg.Roles {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
