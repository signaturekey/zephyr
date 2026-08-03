package routing

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/signaturekey/zephyr/internal/config"
)

func TestRouteImplementationSelectsBaseAndGoRoles(t *testing.T) {
	cfg := mustConfig(t, nil)
	result, err := Route(cfg, Input{Mode: ModeImplementation, ChangedPaths: []string{"main.go"}, HasChanges: true})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}

	want := []string{config.RoleCodeReviewer, config.RoleGolangExpert}
	if got := decisionRoles(result.Selected); !reflect.DeepEqual(got, want) {
		t.Fatalf("selected roles = %v, want %v", got, want)
	}
	if len(result.Excluded) != len(config.KnownRoles())-len(want) {
		t.Fatalf("excluded role count = %d", len(result.Excluded))
	}
	if !hasReason(result.Selected[0], ReasonRequiredByMode) {
		t.Fatalf("base role lacks required reason: %+v", result.Selected[0])
	}
	if reason := reasonWithCode(result.Selected[1], ReasonRoutingRule); reason == nil || len(reason.MatchedPaths) != 1 || reason.MatchedPaths[0] != "main.go" {
		t.Fatalf("Go role lacks path evidence: %+v", result.Selected[1])
	}
}

func TestRouteTypeScriptFrontendSelectsSpecialists(t *testing.T) {
	result, err := Route(mustConfig(t, nil), Input{
		Mode:         ModeImplementation,
		ChangedPaths: []string{"src/pages/profile.tsx", "src/api/profile.ts"},
		Signals:      []string{"typescript", "frontend", "tests"},
		HasChanges:   true,
	})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{
		config.RoleCodeReviewer,
		config.RoleTypeScriptExpert,
		config.RoleFrontendExpert,
		config.RoleQAExpert,
	}
	if got := decisionRoles(result.Selected); !reflect.DeepEqual(got, want) {
		t.Fatalf("selected roles = %v, want %v", got, want)
	}
}

func TestRouteSkillChangesSelectAuthoringExpert(t *testing.T) {
	result, err := Route(mustConfig(t, nil), Input{
		Mode:         ModeImplementation,
		ChangedPaths: []string{"frontend/skills/example/SKILL.md", "frontend/skills/example/evals/evals.json"},
		Signals:      []string{"skill-authoring"},
		HasChanges:   true,
	})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{config.RoleCodeReviewer, config.RoleSkillAuthoringExpert}
	if got := decisionRoles(result.Selected); !reflect.DeepEqual(got, want) {
		t.Fatalf("selected roles = %v, want %v", got, want)
	}
}

func TestRouteOperationalChangesSelectFirstIterationExperts(t *testing.T) {
	result, err := Route(mustConfig(t, nil), Input{
		Mode: ModeImplementation,
		ChangedPaths: []string{
			"internal/resilience/retry.go",
			"internal/databus/consumer.go",
			"deploy/k8s/deployment.yaml",
			"internal/cache/redis.go",
		},
		Signals:    []string{"reliability", "messaging", "infrastructure", "storage"},
		HasChanges: true,
	})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{
		config.RoleCodeReviewer,
		config.RoleReliabilityExpert,
		config.RoleStorageExpert,
		config.RoleMessagingExpert,
		config.RoleInfrastructureExpert,
		config.RoleGolangExpert,
	}
	if got := decisionRoles(result.Selected); !reflect.DeepEqual(got, want) {
		t.Fatalf("selected roles = %v, want %v", got, want)
	}
}

func TestRoutePlanSelectsArchitect(t *testing.T) {
	result, err := Route(mustConfig(t, nil), Input{Mode: ModePlan, HasPlan: true})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if got, want := decisionRoles(result.Selected), []string{config.RoleArchitectReviewer}; !reflect.DeepEqual(got, want) {
		t.Fatalf("selected roles = %v, want %v", got, want)
	}
}

func TestRouteAutoMode(t *testing.T) {
	tests := []struct {
		name     string
		input    Input
		wantMode Mode
		wantErr  bool
	}{
		{name: "plan", input: Input{Mode: ModeAuto, HasPlan: true}, wantMode: ModePlan},
		{name: "implementation", input: Input{Mode: ModeAuto, HasChanges: true}, wantMode: ModeImplementation},
		{name: "paths imply changes", input: Input{Mode: ModeAuto, ChangedPaths: []string{"README.md"}}, wantMode: ModeImplementation},
		{name: "alignment", input: Input{Mode: ModeAuto, HasPlan: true, HasChanges: true}, wantMode: ModeAlignment},
		{name: "empty", input: Input{Mode: ModeAuto}, wantErr: true},
	}

	cfg := mustConfig(t, nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := Route(cfg, test.input)
			if test.wantErr {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("error = %v, want ErrInvalidInput", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			if result.Mode != test.wantMode {
				t.Fatalf("mode = %q, want %q", result.Mode, test.wantMode)
			}
		})
	}
}

func TestRouteStandardSelectsEveryMatchedRole(t *testing.T) {
	input := Input{
		Mode: ModeImplementation,
		ChangedPaths: []string{
			"main.go",
			"migrations/001.sql",
			"brief/openapi/public.yaml",
		},
		Signals:    []string{"security"},
		HasChanges: true,
	}
	result, err := Route(mustConfig(t, nil), input)
	if err != nil {
		t.Fatalf("Route: %v", err)
	}

	want := []string{
		config.RoleCodeReviewer,
		config.RoleSecurityAuditor,
		config.RoleSQLExpert,
		config.RoleContractReviewer,
		config.RoleGolangExpert,
	}
	if got := decisionRoles(result.Selected); !reflect.DeepEqual(got, want) {
		t.Fatalf("selected roles = %v, want %v", got, want)
	}
}

func TestRouteThoroughSelectsEveryMatchedRole(t *testing.T) {
	cfg := mustConfig(t, []byte("version: 1\nprofile: thorough\n"))
	result, err := Route(cfg, Input{
		Mode:         ModeImplementation,
		ChangedPaths: []string{"main.go", "migrations/001.sql", "brief/openapi/public.yaml"},
		Signals:      []string{"security", "architecture", "tests"},
		HasChanges:   true,
	})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{
		config.RoleCodeReviewer,
		config.RoleSecurityAuditor,
		config.RoleSQLExpert,
		config.RoleContractReviewer,
		config.RoleGolangExpert,
		config.RoleArchitectReviewer,
		config.RoleQAExpert,
	}
	if got := decisionRoles(result.Selected); !reflect.DeepEqual(got, want) {
		t.Fatalf("selected roles = %v, want %v", got, want)
	}
}

func TestRouteForceIncludeOutranksDetectedOptionalRole(t *testing.T) {
	cfg := mustConfig(t, []byte("version: 1\nlimits:\n  max_roles_standard: 2\n"))
	result, err := Route(cfg, Input{
		Mode:         ModeImplementation,
		HasChanges:   true,
		Signals:      []string{"security"},
		ForceInclude: []string{config.RoleQAExpert},
	})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	want := []string{config.RoleCodeReviewer, config.RoleQAExpert}
	if got := decisionRoles(result.Selected); !reflect.DeepEqual(got, want) {
		t.Fatalf("selected roles = %v, want %v", got, want)
	}
	security := decisionForRole(result.Excluded, config.RoleSecurityAuditor)
	if security == nil || !hasReason(*security, ReasonProfileLimit) {
		t.Fatalf("security role should be displaced with an explanation: %+v", security)
	}
}

func TestRouteExplicitlyExcludesOptionalRole(t *testing.T) {
	result, err := Route(mustConfig(t, nil), Input{
		Mode:         ModeImplementation,
		ChangedPaths: []string{"main.go"},
		Signals:      []string{"security"},
		ForceExclude: []string{config.RoleSecurityAuditor},
		HasChanges:   true,
	})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if decisionForRole(result.Selected, config.RoleSecurityAuditor) != nil {
		t.Fatal("explicitly excluded role was selected")
	}
	security := decisionForRole(result.Excluded, config.RoleSecurityAuditor)
	if security == nil || !hasReason(*security, ReasonExplicitExclusion) {
		t.Fatalf("excluded role lacks explicit reason: %+v", security)
	}
}

func TestRouteExplainsDisabledMatchedRole(t *testing.T) {
	cfg := mustConfig(t, []byte("version: 1\nroles:\n  golang-expert:\n    enabled: false\n"))
	result, err := Route(cfg, Input{Mode: ModeImplementation, ChangedPaths: []string{"main.go"}, HasChanges: true})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	decision := decisionForRole(result.Excluded, config.RoleGolangExpert)
	if decision == nil || !hasReason(*decision, ReasonRoutingRule) || !hasReason(*decision, ReasonDisabled) {
		t.Fatalf("disabled matched role lacks complete explanation: %+v", decision)
	}
}

func TestRouteRuleWithPathsAndSignalsRequiresBoth(t *testing.T) {
	cfg := mustConfig(t, []byte(`
version: 1
routing:
  - when:
      paths: ["cmd/**"]
      signals: ["public-entrypoint"]
    add_roles: ["qa-expert"]
`))

	withoutSignal, err := Route(cfg, Input{Mode: ModeImplementation, ChangedPaths: []string{"cmd/zephyr/main.go"}, HasChanges: true})
	if err != nil {
		t.Fatal(err)
	}
	if decisionForRole(withoutSignal.Selected, config.RoleQAExpert) != nil {
		t.Fatal("rule matched without its signal group")
	}

	withSignal, err := Route(cfg, Input{Mode: ModeImplementation, ChangedPaths: []string{"cmd/zephyr/main.go"}, Signals: []string{"public-entrypoint"}, HasChanges: true})
	if err != nil {
		t.Fatal(err)
	}
	decision := decisionForRole(withSignal.Selected, config.RoleQAExpert)
	if decision == nil {
		t.Fatal("rule did not match both condition groups")
	}
	reason := reasonWithCode(*decision, ReasonRoutingRule)
	if reason == nil || !reflect.DeepEqual(reason.MatchedPaths, []string{"cmd/zephyr/main.go"}) || !reflect.DeepEqual(reason.MatchedSignals, []string{"public-entrypoint"}) {
		t.Fatalf("unexpected routing evidence: %+v", reason)
	}
}

func TestRouteRejectsInvalidOverridesAndMandatoryExclusion(t *testing.T) {
	tests := []struct {
		name  string
		input Input
		want  string
	}{
		{name: "unknown role", input: Input{Mode: ModePlan, ForceInclude: []string{"oracle"}}, want: "unknown role"},
		{name: "duplicate role", input: Input{Mode: ModePlan, ForceInclude: []string{config.RoleQAExpert, config.RoleQAExpert}}, want: "duplicate role"},
		{name: "conflicting role", input: Input{Mode: ModePlan, ForceInclude: []string{config.RoleQAExpert}, ForceExclude: []string{config.RoleQAExpert}}, want: "both force-included"},
		{name: "mandatory exclusion", input: Input{Mode: ModeImplementation, ForceExclude: []string{config.RoleCodeReviewer}}, want: "requires force-excluded"},
	}

	cfg := mustConfig(t, nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Route(cfg, test.input)
			if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want ErrInvalidInput containing %q", err, test.want)
			}
		})
	}
}

func TestRouteRejectsDisabledRequiredOrForcedRole(t *testing.T) {
	cfg := mustConfig(t, []byte(`
version: 1
roles:
  architect-reviewer:
    enabled: false
  qa-expert:
    enabled: false
`))

	if _, err := Route(cfg, Input{Mode: ModePlan}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "requires disabled") {
		t.Fatalf("required disabled role error = %v", err)
	}
	if _, err := Route(cfg, Input{Mode: ModeImplementation, HasChanges: true, ForceInclude: []string{config.RoleQAExpert}}); !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "force-include disabled") {
		t.Fatalf("forced disabled role error = %v", err)
	}
}

func TestRouteRejectsTooManyMandatoryAndForcedRoles(t *testing.T) {
	cfg := mustConfig(t, []byte("version: 1\nlimits:\n  max_roles_standard: 1\n"))
	_, err := Route(cfg, Input{
		Mode:         ModeImplementation,
		HasChanges:   true,
		ForceInclude: []string{config.RoleQAExpert},
	})
	if !errors.Is(err, ErrInvalidInput) || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("error = %v, want forced-role limit error", err)
	}
}

func TestRouteRejectsNonRepositoryRelativePath(t *testing.T) {
	for _, path := range []string{"/tmp/main.go", "../main.go", "a/../../main.go", ".", "bad\x00path.go"} {
		_, err := Route(mustConfig(t, nil), Input{Mode: ModeImplementation, ChangedPaths: []string{path}, HasChanges: true})
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("path %q error = %v, want ErrInvalidInput", path, err)
		}
	}
}

func TestRouteIsDeterministic(t *testing.T) {
	cfg := mustConfig(t, nil)
	input := Input{
		Mode:         ModeAlignment,
		ChangedPaths: []string{"z.go", "migrations/2.sql", "a.go"},
		Signals:      []string{"tests", "security"},
		HasPlan:      true,
		HasChanges:   true,
	}
	want, err := Route(cfg, input)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := Route(cfg, input)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("route %d was nondeterministic:\n got %#v\nwant %#v", i, got, want)
		}
	}
}

func mustConfig(t *testing.T, project []byte) config.Config {
	t.Helper()
	cfg, err := config.LoadBytes(project)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}

func decisionRoles(decisions []Decision) []string {
	roles := make([]string, len(decisions))
	for i, decision := range decisions {
		roles[i] = decision.Role
	}
	return roles
}

func decisionForRole(decisions []Decision, role string) *Decision {
	for i := range decisions {
		if decisions[i].Role == role {
			return &decisions[i]
		}
	}
	return nil
}

func hasReason(decision Decision, code string) bool { return reasonWithCode(decision, code) != nil }

func reasonWithCode(decision Decision, code string) *Reason {
	for i := range decision.Reasons {
		if decision.Reasons[i].Code == code {
			return &decision.Reasons[i]
		}
	}
	return nil
}
