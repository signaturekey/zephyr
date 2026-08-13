package evals_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardEvaluationRecords(t *testing.T) {
	schemaData, err := os.ReadFile("forward-eval.schema.json")
	require.NoError(t, err)
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	require.NoError(t, err)
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	require.NoError(t, compiler.AddResource("forward-eval.schema.json", document))
	compiled, err := compiler.Compile("forward-eval.schema.json")
	require.NoError(t, err)

	paths, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	require.NoError(t, err)
	casePaths, err := filepath.Glob(filepath.Join("cases", "*.json"))
	require.NoError(t, err)
	paths = append(paths, casePaths...)
	sort.Strings(paths)
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			require.NoError(t, err, "decode JSON")
			require.NoError(t, compiled.Validate(instance), "schema validation")
			validateReferences(t, data)
		})
	}
}

type evalRecord struct {
	Synthetic bool `json:"synthetic"`
	Baseline  struct {
		HumanFindings []struct {
			ID string `json:"id"`
		} `json:"human_findings"`
	} `json:"baseline"`
	ZephyrRun struct {
		Findings []struct {
			ID string `json:"id"`
		} `json:"findings"`
	} `json:"zephyr_run"`
	Comparison struct {
		Matched []struct {
			HumanID   string   `json:"human_id"`
			ZephyrIDs []string `json:"zephyr_ids"`
		} `json:"matched"`
		MissedHumanIDs []string `json:"missed_human_ids"`
		ZephyrOnly     []struct {
			ZephyrID string `json:"zephyr_id"`
		} `json:"zephyr_only"`
	} `json:"comparison"`
}

func validateReferences(t *testing.T, data []byte) {
	t.Helper()
	var record evalRecord
	require.NoError(t, json.Unmarshal(data, &record))
	humanIDs := make([]string, 0, len(record.Baseline.HumanFindings))
	for _, finding := range record.Baseline.HumanFindings {
		humanIDs = append(humanIDs, finding.ID)
	}
	zephyrIDs := make([]string, 0, len(record.ZephyrRun.Findings))
	for _, finding := range record.ZephyrRun.Findings {
		zephyrIDs = append(zephyrIDs, finding.ID)
	}
	human := makeIDSet(t, "human", humanIDs)
	zephyr := makeIDSet(t, "Zephyr", zephyrIDs)
	classifiedHuman := make(map[string]struct{}, len(human))
	classifiedZephyr := make(map[string]struct{}, len(zephyr))
	for _, match := range record.Comparison.Matched {
		requireID(t, human, match.HumanID, "matched human")
		classifyOnce(t, classifiedHuman, match.HumanID, "human")
		for _, id := range match.ZephyrIDs {
			requireID(t, zephyr, id, "matched Zephyr")
			classifyOnce(t, classifiedZephyr, id, "Zephyr")
		}
	}
	for _, id := range record.Comparison.MissedHumanIDs {
		requireID(t, human, id, "missed human")
		classifyOnce(t, classifiedHuman, id, "human")
	}
	for _, item := range record.Comparison.ZephyrOnly {
		requireID(t, zephyr, item.ZephyrID, "Zephyr-only")
		classifyOnce(t, classifiedZephyr, item.ZephyrID, "Zephyr")
	}
	assert.Len(t, classifiedHuman, len(human), "comparison classifies every human finding")
	assert.Len(t, classifiedZephyr, len(zephyr), "comparison classifies every Zephyr finding")
}

func makeIDSet(t *testing.T, label string, values []string) map[string]struct{} {
	t.Helper()
	result := make(map[string]struct{}, len(values))
	for _, id := range values {
		_, exists := result[id]
		require.Falsef(t, exists, "duplicate %s finding ID %q", label, id)
		result[id] = struct{}{}
	}
	return result
}

func requireID(t *testing.T, known map[string]struct{}, id, label string) {
	t.Helper()
	_, exists := known[id]
	assert.Truef(t, exists, "%s reference %q does not exist", label, id)
}

func classifyOnce(t *testing.T, classified map[string]struct{}, id, label string) {
	t.Helper()
	_, exists := classified[id]
	assert.Falsef(t, exists, "%s finding %q is classified more than once", label, id)
	classified[id] = struct{}{}
}
