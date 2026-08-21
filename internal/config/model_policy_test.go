package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveModelPolicyInheritsPartialRoleOverride(t *testing.T) {
	policy, err := ResolveModelPolicy(Config{ModelPolicy: ModelPolicy{
		Default: ModelSettings{Model: "gpt-5.6-terra", Effort: "medium"},
		Stages: ModelPolicyStages{
			Probe: ModelSettings{Model: "gpt-5.6-luna", Effort: "low", Fast: true, fastSet: true},
			Reviewers: ReviewerModelPolicy{
				Default: ModelSettings{Effort: "high", Fast: true, fastSet: true},
				Roles: map[string]ModelSettings{
					RoleSecurityAuditor: {Model: "gpt-5.6-sol"},
				},
			},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveModelPolicy() error = %v", err)
	}

	assertPolicyEntry(t, policy, reviewerProcess(RoleCodeReviewer), ModelSettings{Model: "gpt-5.6-terra", Effort: "high", Fast: true})
	assertPolicyEntry(t, policy, reviewerProcess(RoleSecurityAuditor), ModelSettings{Model: "gpt-5.6-sol", Effort: "high", Fast: true})
	assertPolicyEntry(t, policy, ProcessProbe, ModelSettings{Model: "gpt-5.6-luna", Effort: "low", Fast: true})
}

func TestResolveModelPolicyMarshalsStableProcessOrder(t *testing.T) {
	policy, err := ResolveModelPolicy(Config{ModelPolicy: ModelPolicy{
		Default: ModelSettings{Model: "gpt-5.6-terra", Effort: "medium"},
		Stages: ModelPolicyStages{
			Probe:          ModelSettings{Model: "gpt-5.6-luna", Effort: "low", Fast: true, fastSet: true},
			SemanticRouter: ModelSettings{Effort: "low"},
			Reviewers: ReviewerModelPolicy{
				Default: ModelSettings{Effort: "high", Fast: true, fastSet: true},
			},
			EvidenceGate: ModelSettings{Model: "gpt-5.6-sol", Effort: "xhigh"},
		},
	}})
	if err != nil {
		t.Fatalf("ResolveModelPolicy() error = %v", err)
	}

	data, err := policy.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if got, want := lines[0], "zephyr-codex-model-policy-v1"; got != want {
		t.Fatalf("header = %q, want %q", got, want)
	}
	if got, want := lines[1], "probe\t-\tgpt-5.6-luna\tlow\ttrue"; got != want {
		t.Fatalf("first process = %q, want %q", got, want)
	}
	if got, want := lines[2], "semantic-router\t-\tgpt-5.6-terra\tlow\tfalse"; got != want {
		t.Fatalf("second process = %q, want %q", got, want)
	}
	if got, want := lines[len(lines)-1], "evidence-gate\t-\tgpt-5.6-sol\txhigh\tfalse"; got != want {
		t.Fatalf("last process = %q, want %q", got, want)
	}
}

func TestLoadBytesRejectsInvalidModelPolicy(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "unknown field", yaml: "version: 1\nmodel_policy:\n  default:\n    model: inherit\n    speed: fast\n", want: "field speed not found"},
		{name: "unknown reviewer", yaml: "version: 1\nmodel_policy:\n  stages:\n    reviewers:\n      roles:\n        oracle:\n          model: gpt-5.6-sol\n", want: "unknown reviewer role"},
		{name: "unsafe model", yaml: "version: 1\nmodel_policy:\n  default:\n    model: 'gpt-5.6-sol bad'\n", want: "model must be"},
		{name: "invalid effort", yaml: "version: 1\nmodel_policy:\n  default:\n    effort: extreme\n", want: "effort must be"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(test.yaml))
			if err == nil {
				t.Fatal("LoadBytes() error = nil")
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

func assertPolicyEntry(t *testing.T, policy ResolvedModelPolicy, process string, want ModelSettings) {
	t.Helper()
	got, ok := policy.Entry(process)
	require.True(t, ok, "policy entry %q missing", process)
	assert.Equal(t, want.Model, got.Model, "policy entry %q model", process)
	assert.Equal(t, want.Effort, got.Effort, "policy entry %q effort", process)
	assert.Equal(t, want.Fast, got.Fast, "policy entry %q fast", process)
}
