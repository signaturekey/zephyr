package run

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveMode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		mode       Mode
		hasPlan    bool
		hasChanges bool
		want       Mode
		wantError  bool
	}{
		{name: "explicit", mode: ModeImplementation, want: ModeImplementation},
		{name: "auto plan", mode: ModeAuto, hasPlan: true, want: ModePlan},
		{name: "auto implementation", mode: ModeAuto, hasChanges: true, want: ModeImplementation},
		{name: "auto alignment", mode: ModeAuto, hasPlan: true, hasChanges: true, want: ModeAlignment},
		{name: "auto empty", mode: ModeAuto, wantError: true},
		{name: "invalid", mode: Mode("mystery"), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveMode(test.mode, test.hasPlan, test.hasChanges)
			if test.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, test.want, got)
		})
	}
}

func TestManifestSetStage(t *testing.T) {
	t.Parallel()
	manifest := Manifest{Stages: defaultStages(testTime)}
	require.NoError(t, manifest.SetStage("collect", StageRunning, testTime, ""))
	require.NoError(t, manifest.SetStage("collect", StageComplete, testTime.Add(1), ""))
	assert.NotNil(t, manifest.Stages[1].StartedAt)
	assert.NotNil(t, manifest.Stages[1].FinishedAt)
	assert.Error(t, manifest.SetStage("unknown", StageComplete, testTime, ""))
}
