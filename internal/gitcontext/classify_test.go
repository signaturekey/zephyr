package gitcontext

import (
	"slices"
	"testing"
)

func TestSafeStatePathspecsExcludeExactRestrictedNames(t *testing.T) {
	status := WorktreeStatus{Entries: []StatusEntry{
		{Path: "CLIENT.P12", Restricted: true},
		{Path: "renamed.txt", PreviousPath: "SECRET.PEM", Restricted: true},
		{Path: "internal/service.go"},
	}}

	got := safeStatePathspecs(status, nil)
	for _, want := range []string{
		":(top,exclude,literal)CLIENT.P12",
		":(top,exclude,literal)renamed.txt",
		":(top,exclude,literal)SECRET.PEM",
	} {
		if !slices.Contains(got, want) {
			t.Fatalf("safe state pathspecs do not contain %q: %#v", want, got)
		}
	}
	if slices.Contains(got, ":(top,exclude,literal)internal/service.go") {
		t.Fatalf("non-restricted path was excluded: %#v", got)
	}
}

func TestGeneratedTypeScriptPaths(t *testing.T) {
	for _, path := range []string{
		"generated/client.ts",
		"src/__generated__/types.ts",
		"src/api/client.gen.ts",
		"src/api/client.generated.tsx",
	} {
		if !isGeneratedPath(path) {
			t.Errorf("%q was not classified as generated", path)
		}
	}
	for _, path := range []string{"src/client.ts", "src/types.d.ts", "src/component.tsx"} {
		if isGeneratedPath(path) {
			t.Errorf("%q was incorrectly classified as generated", path)
		}
	}
}
