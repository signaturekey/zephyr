package run

import "testing"

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
			if (err != nil) != test.wantError {
				t.Fatalf("ResolveMode() error = %v, wantError %v", err, test.wantError)
			}
			if got != test.want {
				t.Fatalf("ResolveMode() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestManifestSetStage(t *testing.T) {
	t.Parallel()
	manifest := Manifest{Stages: defaultStages(testTime)}
	if err := manifest.SetStage("collect", StageRunning, testTime, ""); err != nil {
		t.Fatal(err)
	}
	if err := manifest.SetStage("collect", StageComplete, testTime.Add(1), ""); err != nil {
		t.Fatal(err)
	}
	if manifest.Stages[1].StartedAt == nil || manifest.Stages[1].FinishedAt == nil {
		t.Fatal("collect stage timestamps were not recorded")
	}
	if err := manifest.SetStage("unknown", StageComplete, testTime, ""); err == nil {
		t.Fatal("SetStage() accepted an unknown stage")
	}
}
