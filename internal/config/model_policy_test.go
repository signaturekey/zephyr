package config

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveModelPolicyDefaultsUseTieredModels(t *testing.T) {
	cfg, err := LoadBytes(nil)
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}

	policy, err := ResolveModelPolicy(cfg)
	if err != nil {
		t.Fatalf("ResolveModelPolicy() error = %v", err)
	}

	assertPolicyEntry(t, policy, ProcessProbe, ModelSettings{Model: "gpt-5.6-luna", Effort: "low", Fast: true})
	assertPolicyEntry(t, policy, ProcessSemanticRouter, ModelSettings{Model: "gpt-5.6-terra", Effort: "low", Fast: false})
	assertPolicyEntry(t, policy, reviewerProcess(RoleCodeReviewer), ModelSettings{Model: "gpt-5.6-terra", Effort: "medium", Fast: false})
	assertPolicyEntry(t, policy, reviewerProcess(RoleSkillAuthoringExpert), ModelSettings{Model: "gpt-5.6-terra", Effort: "medium", Fast: false})
	assertPolicyEntry(t, policy, reviewerProcess(RoleCodeSimplifier), ModelSettings{Model: "gpt-5.6-terra", Effort: "low", Fast: false})
	assertPolicyEntry(t, policy, reviewerProcess(RoleSecurityAuditor), ModelSettings{Model: "gpt-5.6-sol", Effort: "high", Fast: false})
	assertPolicyEntry(t, policy, ProcessEvidenceGate, ModelSettings{Model: "gpt-5.6-sol", Effort: "high", Fast: false})
}

func TestResolveModelPolicyInheritsPartialRoleOverride(t *testing.T) {
	cfg, err := LoadBytes([]byte(`
version: 1
model_policy:
  default:
    model: gpt-5.6-terra
    effort: medium
    fast: false
  stages:
    reviewers:
      default:
        effort: high
        fast: true
      roles:
        security-auditor:
          model: gpt-5.6-sol
`))
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}

	policy, err := ResolveModelPolicy(cfg)
	if err != nil {
		t.Fatalf("ResolveModelPolicy() error = %v", err)
	}

	assertPolicyEntry(t, policy, reviewerProcess(RoleCodeReviewer), ModelSettings{Model: "gpt-5.6-terra", Effort: "high", Fast: true})
	assertPolicyEntry(t, policy, reviewerProcess(RoleSecurityAuditor), ModelSettings{Model: "gpt-5.6-sol", Effort: "high", Fast: true})
	assertPolicyEntry(t, policy, ProcessProbe, ModelSettings{Model: "gpt-5.6-luna", Effort: "low", Fast: true})
}

func TestResolveModelPolicyMarshalsStableProcessOrder(t *testing.T) {
	cfg, err := LoadBytes(nil)
	if err != nil {
		t.Fatalf("LoadBytes() error = %v", err)
	}
	policy, err := ResolveModelPolicy(cfg)
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
	if got, want := lines[len(lines)-1], "evidence-gate\t-\tgpt-5.6-sol\thigh\tfalse"; got != want {
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
	if !ok || got.Model != want.Model || got.Effort != want.Effort || got.Fast != want.Fast {
		t.Fatalf("Entry(%q) = %+v, found=%v, want %+v", process, got, ok, want)
	}
}
