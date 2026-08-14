package evals_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type skillTriggerCases struct {
	Positive []struct {
		Request                  string `json:"request"`
		ActivationPhrase         string `json:"activation_phrase"`
		ExpectedDriverActivation bool   `json:"expected_driver_activation"`
	} `json:"positive"`
	Negative []struct {
		Request                  string `json:"request"`
		ExpectedDriverActivation bool   `json:"expected_driver_activation"`
	} `json:"negative"`
}

func TestSkillTriggerCases(t *testing.T) {
	fixture, err := os.ReadFile("skill-trigger-cases.json")
	require.NoError(t, err)
	var cases skillTriggerCases
	require.NoError(t, json.Unmarshal(fixture, &cases))
	require.NotEmpty(t, cases.Positive)
	require.NotEmpty(t, cases.Negative)

	skillPath := filepath.Join("..", "harnesses", "codex", "SKILL.md")
	skill, err := os.ReadFile(skillPath)
	require.NoError(t, err)
	sectionStart := strings.Index(string(skill), "## Experimental local driver (explicit opt-in)")
	require.GreaterOrEqual(t, sectionStart, 0, "the installed skill must contain an opt-in driver section")
	sectionEnd := strings.Index(string(skill[sectionStart+1:]), "\n## ")
	if sectionEnd >= 0 {
		sectionEnd += sectionStart + 1
	} else {
		sectionEnd = len(skill)
	}
	experimental := string(skill[sectionStart:sectionEnd])
	require.Contains(t, experimental, "only when the user explicitly names")
	require.Contains(t, experimental, "Ordinary\nZephyr plan, code, and PR review requests must keep the choreography below")
	require.NotContains(t, string(skill[:sectionStart]), "zephyr-codex", "the standard activation path must not invoke the driver")

	for _, positive := range cases.Positive {
		t.Run("positive/"+positive.ActivationPhrase, func(t *testing.T) {
			require.NotEmpty(t, positive.Request)
			require.Contains(t, strings.ToLower(positive.Request), strings.ToLower(positive.ActivationPhrase))
			require.Contains(t, experimental, positive.ActivationPhrase)
			require.Equal(t, positive.ExpectedDriverActivation, experimentalDriverRequested(positive.Request))
		})
	}
	for _, negative := range cases.Negative {
		t.Run("negative/"+positiveName(negative.Request), func(t *testing.T) {
			require.NotEmpty(t, negative.Request)
			request := strings.ToLower(negative.Request)
			for _, positive := range cases.Positive {
				require.NotContains(t, request, strings.ToLower(positive.ActivationPhrase))
			}
			require.Equal(t, negative.ExpectedDriverActivation, experimentalDriverRequested(negative.Request))
		})
	}
}

func positiveName(request string) string {
	return strings.NewReplacer(" ", "-", "/", "-", ".", "").Replace(request)
}

func experimentalDriverRequested(request string) bool {
	normalized := strings.ToLower(request)
	for _, explicit := range []string{
		"zephyr-codex doctor",
		"zephyr-codex review",
		"experimental local driver",
	} {
		if strings.Contains(normalized, explicit) {
			return true
		}
	}
	return false
}
