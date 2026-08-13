package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	require.NoError(t, err, "load defaults")

	if cfg.Version != CurrentVersion || cfg.Profile != ProfileStandard || cfg.Language != "auto" {
		t.Fatalf("unexpected defaults: version=%d profile=%q language=%q", cfg.Version, cfg.Profile, cfg.Language)
	}
	wantLimits := Limits{MaxParallelReviewers: 8, MaxRolesStandard: 17, MaxRolesThorough: 17, MaxFinalFindings: 30}
	if cfg.Limits != wantLimits {
		t.Fatalf("limits = %+v, want %+v", cfg.Limits, wantLimits)
	}
	for _, role := range KnownRoles() {
		roleConfig, ok := cfg.Roles[role]
		if !ok || !roleConfig.Enabled {
			assert.True(t, ok && roleConfig.Enabled, "default role %q is not enabled", role)
		}
	}
	if roleConfig, ok := cfg.Roles["python-expert"]; !ok || !roleConfig.Enabled {
		t.Error("default python-expert role is not enabled")
	}
	if roleConfig, ok := cfg.Roles[RoleReactExpert]; !ok || !roleConfig.Enabled {
		t.Error("default react-expert role is not enabled")
	}
	if len(cfg.Routing) != 26 {
		t.Fatalf("routing rule count = %d, want 26", len(cfg.Routing))
	}
	if !cfg.Redaction.Enabled || len(cfg.Redaction.DenyPatterns) != 3 {
		t.Fatalf("unexpected redaction defaults: %+v", cfg.Redaction)
	}
}

func TestLoadBytesMergesProjectConfig(t *testing.T) {
	project := []byte(`
version: 1
profile: thorough
limits:
  max_parallel_reviewers: 4
  max_final_findings: 12
roles:
  code-simplifier:
    enabled: false
routing:
  - when:
      paths: ["cmd/**"]
      signals: ["public-entrypoint"]
    add_roles: ["architect-reviewer"]
restricted_paths: ["third_party/**"]
redaction:
  enabled: false
  deny_patterns: ["**/*.key"]
`)

	cfg, err := LoadBytes(project)
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if cfg.Profile != ProfileThorough {
		t.Errorf("profile = %q, want thorough", cfg.Profile)
	}
	if cfg.Limits.MaxParallelReviewers != 4 || cfg.Limits.MaxFinalFindings != 12 {
		t.Errorf("project limits not applied: %+v", cfg.Limits)
	}
	if cfg.Limits.MaxRolesStandard != 17 || cfg.Limits.MaxRolesThorough != 17 {
		t.Errorf("unmentioned default limits were lost: %+v", cfg.Limits)
	}
	if cfg.Roles[RoleCodeSimplifier].Enabled {
		t.Error("explicit false role override was not preserved")
	}
	if !cfg.Roles[RoleCodeReviewer].Enabled {
		t.Error("unmentioned default role was lost")
	}
	if len(cfg.Routing) != 27 {
		t.Fatalf("routing rule count = %d, want defaults plus project rule", len(cfg.Routing))
	}
	if !contains(cfg.RestrictedPaths, "vendor/**") || !contains(cfg.RestrictedPaths, "third_party/**") {
		t.Fatalf("restricted paths were not appended: %v", cfg.RestrictedPaths)
	}
	if cfg.Redaction.Enabled || !contains(cfg.Redaction.DenyPatterns, "**/*.pem") || !contains(cfg.Redaction.DenyPatterns, "**/*.key") {
		t.Fatalf("redaction merge = %+v", cfg.Redaction)
	}
}

func TestLoadSupportsFileAndRepositoryDirectory(t *testing.T) {
	repository := t.TempDir()
	configDir := filepath.Join(repository, ".zephyr")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	configPath := filepath.Join(configDir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("version: 1\nprofile: thorough\n"), 0o600))

	fromDir, err := Load(repository)
	require.NoError(t, err, "load repository directory")
	fromFile, err := Load(configPath)
	require.NoError(t, err, "load file")
	if diff := cmp.Diff(fromFile, fromDir, cmp.AllowUnexported(ModelSettings{})); diff != "" {
		t.Fatalf("loaded config mismatch (-want +got):\n%s", diff)
	}
}

func TestEmbeddedDefaultsDoNotDependOnWorkingDirectory(t *testing.T) {
	original, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(t.TempDir()))
	t.Cleanup(func() { _ = os.Chdir(original) })

	if _, err := Load(""); err != nil {
		t.Fatalf("Load defaults outside checkout: %v", err)
	}
}

func TestLoadBytesRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		project string
		want    string
	}{
		{name: "unknown field", project: "version: 1\nprofiel: standard\n", want: "field profiel not found"},
		{name: "future version", project: "version: 2\n", want: "version must be 1"},
		{name: "profile", project: "version: 1\nprofile: maximal\n", want: "profile must be"},
		{name: "language", project: "version: 1\nlanguage: rust\n", want: "language must be"},
		{name: "parallel exceeds thorough", project: "version: 1\nlimits:\n  max_parallel_reviewers: 18\n", want: "cannot exceed"},
		{name: "unknown role", project: "version: 1\nroles:\n  oracle:\n    enabled: true\n", want: "unknown role"},
		{name: "unknown role field", project: "version: 1\nroles:\n  golang-expert:\n    active: true\n", want: "field active not found"},
		{name: "empty routing condition", project: "version: 1\nrouting:\n  - when: {}\n    add_roles: [golang-expert]\n", want: "must contain paths or signals"},
		{name: "invalid glob", project: "version: 1\nrouting:\n  - when:\n      paths: ['[']\n    add_roles: [golang-expert]\n", want: "invalid glob"},
		{name: "invalid signal", project: "version: 1\nrouting:\n  - when:\n      signals: ['Not Valid']\n    add_roles: [golang-expert]\n", want: "kebab-case"},
		{name: "unknown routed role", project: "version: 1\nrouting:\n  - when:\n      signals: [go]\n    add_roles: [oracle]\n", want: "unknown role"},
		{name: "duplicate routed role", project: "version: 1\nrouting:\n  - when:\n      signals: [go]\n    add_roles: [golang-expert, golang-expert]\n", want: "duplicate role"},
		{name: "multiple documents", project: "version: 1\n---\nprofile: thorough\n", want: "multiple YAML documents"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(test.project))
			if err == nil {
				t.Fatal("LoadBytes unexpectedly succeeded")
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error %q does not wrap ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %q does not contain %q", err, test.want)
			}
		})
	}
}

func TestLoadBytesDeduplicatesAppendedPathPolicies(t *testing.T) {
	cfg, err := LoadBytes([]byte(`
version: 1
restricted_paths: ["vendor/**", "custom/**", "custom/**"]
redaction:
  deny_patterns: ["**/*.pem", "**/*.token", "**/*.token"]
`))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if count(cfg.RestrictedPaths, "vendor/**") != 1 || count(cfg.RestrictedPaths, "custom/**") != 1 {
		t.Fatalf("restricted paths not deduplicated: %v", cfg.RestrictedPaths)
	}
	if count(cfg.Redaction.DenyPatterns, "**/*.pem") != 1 || count(cfg.Redaction.DenyPatterns, "**/*.token") != 1 {
		t.Fatalf("deny patterns not deduplicated: %v", cfg.Redaction.DenyPatterns)
	}
}

func contains(values []string, target string) bool { return count(values, target) != 0 }

func count(values []string, target string) int {
	result := 0
	for _, value := range values {
		if value == target {
			result++
		}
	}
	return result
}
