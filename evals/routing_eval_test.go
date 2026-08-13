package evals_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(paths), 3, "semantic routing eval coverage is incomplete")
	sort.Strings(paths)
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			fixture := loadRoutingEval(t, path)
			cfg, err := config.Load("")
			require.NoError(t, err)
			request, err := routing.PrepareSemantic(cfg, routing.Input{
				Mode: routing.Mode(fixture.Mode), ChangedPaths: fixture.ChangedPaths,
				Signals: fixture.Signals, StrongSignals: fixture.StrongSignals,
				HasPlan: fixture.HasPlan, HasChanges: fixture.HasChanges,
			}, fixture.CaseID, strings.Repeat("a", 64), []routing.EvidenceSource{{ID: "scope", Kind: "scope", Source: "eval packet"}})
			require.NoError(t, err)
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
			require.NoError(t, err)
			assertCompleteRoutingProvenance(t, request, result)
			selected := routingRoles(result.Selected)
			if diff := cmp.Diff(fixture.ExpectedSelectedRoles, selected); diff != "" {
				t.Fatalf("selected roles mismatch (-want +got):\n%s", diff)
			}
			excluded := routingRoles(result.Excluded)
			for _, role := range fixture.ExpectedExcludedRoles {
				assert.Contains(t, excluded, role)
			}
			second, err := routing.ResolveSemantic(request, proposal)
			require.NoError(t, err)
			firstJSON, err := json.Marshal(result)
			require.NoError(t, err)
			secondJSON, err := json.Marshal(second)
			require.NoError(t, err)
			assert.Equal(t, firstJSON, secondJSON, "same semantic response produced different routing.json")
		})
	}
}

func TestLiveSemanticRoutingClassification(t *testing.T) {
	harness := os.Getenv("ZEPHYR_ROUTING_EVAL_HARNESS")
	if harness == "" {
		t.Skip("set ZEPHYR_ROUTING_EVAL_HARNESS=codex to run live semantic classification evals")
	}
	require.Equalf(t, "codex", harness, "unsupported ZEPHYR_ROUTING_EVAL_HARNESS %q", harness)
	dispatcher, err := filepath.Abs(filepath.Join("..", "harnesses", harness, "dispatch.sh"))
	require.NoError(t, err)
	compatibility := os.Getenv("ZEPHYR_ROUTING_EVAL_COMPAT")
	if harness == "codex" && compatibility == "" {
		t.Skip("set ZEPHYR_ROUTING_EVAL_COMPAT to a fresh Codex compatibility descriptor")
	}

	paths, err := filepath.Glob(filepath.Join("routing-cases", "*.json"))
	require.NoError(t, err)
	sort.Strings(paths)
	for _, path := range paths {
		fixture := loadRoutingEval(t, path)
		t.Run(filepath.Base(path), func(t *testing.T) {
			cfg, err := config.Load("")
			require.NoError(t, err)
			request, err := routing.PrepareSemantic(cfg, routing.Input{
				Mode: routing.Mode(fixture.Mode), ChangedPaths: fixture.ChangedPaths,
				Signals: fixture.Signals, StrongSignals: fixture.StrongSignals,
				HasPlan: fixture.HasPlan, HasChanges: fixture.HasChanges,
			}, fixture.CaseID, strings.Repeat("a", 64), []routing.EvidenceSource{{ID: "scope", Kind: "scope", Source: "eval scope"}})
			require.NoError(t, err)
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
			output, err := command.CombinedOutput()
			require.NoErrorf(t, err, "semantic router dispatch failed: %s", output)
			output, err = os.ReadFile(outputPath)
			require.NoError(t, err)
			proposal, err := schema.ValidateSemanticRoutingBytes(output)
			require.NoError(t, err)
			result, err := routing.ResolveSemantic(request, proposal)
			require.NoError(t, err)
			if diff := cmp.Diff(fixture.ExpectedSelectedRoles, routingRoles(result.Selected)); diff != "" {
				t.Fatalf("live semantic roles mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func assertCompleteRoutingProvenance(t *testing.T, request routing.SemanticRequest, result routing.Result) {
	t.Helper()
	seen := make(map[string]struct{}, len(config.KnownRoles()))
	for _, decision := range append(append([]routing.Decision{}, result.Selected...), result.Excluded...) {
		_, duplicate := seen[decision.Role]
		require.Falsef(t, duplicate, "role %q appears more than once in routing result", decision.Role)
		seen[decision.Role] = struct{}{}
		require.NotEmptyf(t, decision.Source, "role %q lacks source", decision.Role)
		require.NotEmptyf(t, decision.Reasons, "role %q lacks reasons", decision.Role)
	}
	assert.Len(t, seen, len(config.KnownRoles()), "routing accounts for every known role")
	for _, decision := range request.Protected {
		assert.True(t, decision.Protected, "protected decision lacks protection")
		assert.NotEmpty(t, decision.Source, "protected decision lacks provenance")
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
		require.NotNilf(t, resolved, "semantic candidate %q lacks semantic provenance", candidate.Role)
		require.Equal(t, "semantic-llm", resolved.Source)
		semanticReason := resolved.Reasons[len(resolved.Reasons)-1]
		assert.NotEmpty(t, semanticReason.EvidenceRefs, "semantic candidate %q lacks evidence provenance", candidate.Role)
		assert.NotNil(t, semanticReason.Confidence, "semantic candidate %q lacks confidence", candidate.Role)
	}
}

func loadRoutingEval(t *testing.T, path string) routingEvalCase {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result routingEvalCase
	require.NoError(t, decoder.Decode(&result))
	var trailing any
	require.ErrorIs(t, decoder.Decode(&trailing), io.EOF, "routing eval contains trailing JSON")
	require.NotEmpty(t, result.CaseID, "routing eval identity is incomplete")
	require.NotEmpty(t, result.Mode, "routing eval identity is incomplete")
	require.NotEmpty(t, strings.TrimSpace(result.ScopeText), "routing eval identity is incomplete")
	return result
}

func routingRoles(decisions []routing.Decision) []string {
	roles := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		roles = append(roles, decision.Role)
	}
	return roles
}
