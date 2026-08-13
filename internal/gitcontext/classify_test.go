package gitcontext

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
		assert.Contains(t, got, want, "safe state pathspecs")
	}
	assert.NotContains(t, got, ":(top,exclude,literal)internal/service.go", "non-restricted path was excluded")
}

func TestGeneratedTypeScriptPaths(t *testing.T) {
	for _, path := range []string{
		"generated/client.ts",
		"src/__generated__/types.ts",
		"src/api/client.gen.ts",
		"src/api/client.generated.tsx",
	} {
		assert.True(t, isGeneratedPath(path), "%q was not classified as generated", path)
	}
	for _, path := range []string{"src/client.ts", "src/types.d.ts", "src/component.tsx"} {
		assert.False(t, isGeneratedPath(path), "%q was incorrectly classified as generated", path)
	}
}
