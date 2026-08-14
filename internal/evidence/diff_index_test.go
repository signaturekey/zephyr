package evidence

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiffLineIndexHandlesQuotedPathsAndMultipleHunks(t *testing.T) {
	diff := "diff --git \"a/path with space.go\" \"b/path with space.go\"\n" +
		"--- \"a/path with space.go\"\n+++ \"b/path with space.go\"\n" +
		"@@ -1,2 +3,2 @@\n a\n+b\n@@ -10 +12,3 @@\n+c\n+d\n+e\n"
	assert.True(t, diffContainsLineRange(diff, "path with space.go", 3, 4), "first immutable hunk was not indexed")
	assert.True(t, diffContainsLineRange(diff, "path with space.go", 12, 14), "second immutable hunk was not indexed")
	assert.False(t, diffContainsLineRange(diff, "path with space.go", 5, 12), "range crossing absent lines unexpectedly matched")
}

func TestDiffLineIndexUsesOldSideForDeletedFile(t *testing.T) {
	diff := "--- a/deleted.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n"
	assert.True(t, diffContainsLineRange(diff, "deleted.go", 1, 1), "deleted source line was not indexed")
}

func TestDiffLineIndexDoesNotTrustTruncatedHunkCount(t *testing.T) {
	diff := "--- a/main.go\n+++ b/main.go\n@@ -1,100 +1,100 @@\n first\n+second\n[ZEPHYR TRUNCATED]\n"
	assert.True(t, diffContainsLineRange(diff, "main.go", 1, 2), "present immutable lines were not indexed")
	assert.False(t, diffContainsLineRange(diff, "main.go", 3, 100), "declared but absent hunk lines unexpectedly matched")
}

func TestExtractDiffHunksPreservesFrozenSourceOrder(t *testing.T) {
	diff := "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n\n@@ -10 +10 @@\n-oldTwo\n+newTwo\n"
	hunks := extractDiffHunks(diff)
	assert.Equal(t, []diffHunk{
		{path: "main.go", start: 1, end: 1, text: "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n"},
		{path: "main.go", start: 10, end: 10, text: "--- a/main.go\n+++ b/main.go\n@@ -10 +10 @@\n-oldTwo\n+newTwo\n"},
	}, hunks)
}

func TestDiffLineIndexDoesNotTreatAddedTriplePlusLineAsFileHeader(t *testing.T) {
	diff := "--- a/main.go\n+++ b/main.go\n@@ -1 +1,2 @@\n old\n+++ addedContent\n\n@@ -10 +11 @@\n-oldLater\n+newLater\n"
	assert.True(t, diffContainsLineRange(diff, "main.go", 11, 11))
}
