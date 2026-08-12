package evals_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/schema"
)

type routingEvalCase struct {
	CaseID                string   `json:"case_id"`
	Mode                  string   `json:"mode"`
	HasPlan               bool     `json:"has_plan"`
	HasChanges            bool     `json:"has_changes"`
	ChangedPaths          []string `json:"changed_paths"`
	Signals               []string `json:"signals"`
	StrongSignals         []string `json:"strong_signals"`
	ScopeText             string   `json:"scope_text"`
	SemanticIncludedRoles []string `json:"semantic_included_roles"`
	ExpectedSelectedRoles []string `json:"expected_selected_roles"`
	ExpectedExcludedRoles []string `json:"expected_excluded_roles"`
}

func TestSemanticRoutingResolutionFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("routing-cases", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 3 {
		t.Fatalf("semantic routing eval coverage is incomplete: %d cases", len(paths))
	}
	sort.Strings(paths)
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			fixture := loadRoutingEval(t, path)
			cfg, err := config.Load("")
			if err != nil {
				t.Fatal(err)
			}
			request, err := routing.PrepareSemantic(cfg, routing.Input{
				Mode: routing.Mode(fixture.Mode), ChangedPaths: fixture.ChangedPaths,
				Signals: fixture.Signals, StrongSignals: fixture.StrongSignals,
				HasPlan: fixture.HasPlan, HasChanges: fixture.HasChanges,
			}, fixture.CaseID, strings.Repeat("a", 64), []routing.EvidenceSource{{ID: "scope", Kind: "scope", Source: "eval packet"}})
			if err != nil {
				t.Fatal(err)
			}
			included := make(map[string]struct{}, len(fixture.SemanticIncludedRoles))
			for _, role := range fixture.SemanticIncludedRoles {
				included[role] = struct{}{}
			}
			proposal := schema.SemanticRoutingEnvelope{Version: routing.SemanticRoutingVersion, RunID: fixture.CaseID, Decisions: []schema.SemanticRoutingDecision{}}
			for _, candidate := range request.Candidates {
				decision := "exclude"
				if _, ok := included[candidate.Role]; ok {
					decision = "include"
				}
				proposal.Decisions = append(proposal.Decisions, schema.SemanticRoutingDecision{
					Role: candidate.Role, Decision: decision, EvidenceRefs: []string{"scope"}, Reason: "eval classification", Confidence: 0.9,
				})
			}
			result, err := routing.ResolveSemantic(request, proposal)
			if err != nil {
				t.Fatal(err)
			}
			assertCompleteRoutingProvenance(t, request, result)
			selected := routingRoles(result.Selected)
			if !slices.Equal(selected, fixture.ExpectedSelectedRoles) {
				t.Fatalf("selected roles = %v, want %v", selected, fixture.ExpectedSelectedRoles)
			}
			excluded := routingRoles(result.Excluded)
			for _, role := range fixture.ExpectedExcludedRoles {
				if !slices.Contains(excluded, role) {
					t.Fatalf("expected excluded role %q missing from %v", role, excluded)
				}
			}
			second, err := routing.ResolveSemantic(request, proposal)
			if err != nil {
				t.Fatal(err)
			}
			firstJSON, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			secondJSON, err := json.Marshal(second)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(firstJSON, secondJSON) {
				t.Fatalf("same semantic response produced different routing.json:\n%s\n%s", firstJSON, secondJSON)
			}
		})
	}
}

func TestLiveSemanticRoutingClassification(t *testing.T) {
	harness := os.Getenv("ZEPHYR_ROUTING_EVAL_HARNESS")
	if harness == "" {
		t.Skip("set ZEPHYR_ROUTING_EVAL_HARNESS=codex to run live semantic classification evals")
	}
	if harness != "codex" {
		t.Fatalf("unsupported ZEPHYR_ROUTING_EVAL_HARNESS %q", harness)
	}
	dispatcher, err := filepath.Abs(filepath.Join("..", "harnesses", harness, "dispatch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	compatibility := os.Getenv("ZEPHYR_ROUTING_EVAL_COMPAT")
	if harness == "codex" && compatibility == "" {
		t.Skip("set ZEPHYR_ROUTING_EVAL_COMPAT to a fresh Codex compatibility descriptor")
	}

	paths, err := filepath.Glob(filepath.Join("routing-cases", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fixture := loadRoutingEval(t, path)
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg, err := config.Load("")
			if err != nil {
				t.Fatal(err)
			}
			request, err := routing.PrepareSemantic(cfg, routing.Input{
				Mode: routing.Mode(fixture.Mode), ChangedPaths: fixture.ChangedPaths,
				Signals: fixture.Signals, StrongSignals: fixture.StrongSignals,
				HasPlan: fixture.HasPlan, HasChanges: fixture.HasChanges,
			}, fixture.CaseID, strings.Repeat("a", 64), []routing.EvidenceSource{{ID: "scope", Kind: "scope", Source: "eval scope"}})
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			packetPath := filepath.Join(root, "review-packet.json")
			requestPath := filepath.Join(root, "routing-request.json")
			outputPath := filepath.Join(root, "routing-output.json")
			packet := map[string]any{
				"version": 1, "run_id": fixture.CaseID, "mode": fixture.Mode,
				"source": "eval", "changed_files": fixture.ChangedPaths,
				"routing_signals": fixture.Signals, "scope": fixture.ScopeText,
				"coverage_limits": []string{},
			}
			writeJSONFile(t, packetPath, packet)
			writeJSONFile(t, requestPath, request)
			args := []string{dispatcher, "routing", "--packet", packetPath, "--request", requestPath}
			if harness == "codex" {
				args = append(args, "--compat", compatibility)
			}
			args = append(args, "--output", outputPath)
			command := exec.Command("sh", args...)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("semantic router dispatch failed: %v\n%s", err, output)
			}
			output, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := schema.ValidateSemanticRoutingBytes(output)
			if err != nil {
				t.Fatal(err)
			}
			result, err := routing.ResolveSemantic(request, proposal)
			if err != nil {
				t.Fatal(err)
			}
			if selected := routingRoles(result.Selected); !slices.Equal(selected, fixture.ExpectedSelectedRoles) {
				t.Fatalf("live semantic roles = %v, want %v", selected, fixture.ExpectedSelectedRoles)
			}
		})
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertCompleteRoutingProvenance(t *testing.T, request routing.SemanticRequest, result routing.Result) {
	t.Helper()
	seen := make(map[string]struct{}, len(config.KnownRoles()))
	for _, decision := range append(append([]routing.Decision{}, result.Selected...), result.Excluded...) {
		if _, duplicate := seen[decision.Role]; duplicate {
			t.Fatalf("role %q appears more than once in routing result", decision.Role)
		}
		seen[decision.Role] = struct{}{}
		if decision.Source == "" || len(decision.Reasons) == 0 {
			t.Fatalf("role %q lacks source or reasons: %#v", decision.Role, decision)
		}
	}
	if len(seen) != len(config.KnownRoles()) {
		t.Fatalf("routing accounts for %d of %d roles", len(seen), len(config.KnownRoles()))
	}
	for _, decision := range request.Protected {
		if !decision.Protected || decision.Source == "" {
			t.Fatalf("protected decision lacks provenance: %#v", decision)
		}
	}
	for _, candidate := range request.Candidates {
		var resolved *routing.Decision
		for i := range result.Selected {
			if result.Selected[i].Role == candidate.Role {
				resolved = &result.Selected[i]
			}
		}
		for i := range result.Excluded {
			if result.Excluded[i].Role == candidate.Role {
				resolved = &result.Excluded[i]
			}
		}
		if resolved == nil || resolved.Source != "semantic-llm" {
			t.Fatalf("semantic candidate %q lacks semantic provenance: %#v", candidate.Role, resolved)
		}
		semanticReason := resolved.Reasons[len(resolved.Reasons)-1]
		if len(semanticReason.EvidenceRefs) == 0 || semanticReason.Confidence == nil {
			t.Fatalf("semantic candidate %q lacks evidence provenance: %#v", candidate.Role, semanticReason)
		}
	}
}

func loadRoutingEval(t *testing.T, path string) routingEvalCase {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result routingEvalCase
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("routing eval contains trailing JSON: %v", err)
	}
	if result.CaseID == "" || result.Mode == "" || strings.TrimSpace(result.ScopeText) == "" {
		t.Fatal("routing eval identity is incomplete")
	}
	return result
}

func routingRoles(decisions []routing.Decision) []string {
	roles := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		roles = append(roles, decision.Role)
	}
	return roles
}
