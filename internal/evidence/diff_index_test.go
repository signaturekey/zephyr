package evidence

import "testing"

func TestDiffLineIndexHandlesQuotedPathsAndMultipleHunks(t *testing.T) {
	diff := "diff --git \"a/path with space.go\" \"b/path with space.go\"\n" +
		"--- \"a/path with space.go\"\n+++ \"b/path with space.go\"\n" +
		"@@ -1,2 +3,2 @@\n a\n+b\n@@ -10 +12,3 @@\n+c\n+d\n+e\n"
	if !diffContainsLineRange(diff, "path with space.go", 3, 4) {
		t.Fatal("first immutable hunk was not indexed")
	}
	if !diffContainsLineRange(diff, "path with space.go", 12, 14) {
		t.Fatal("second immutable hunk was not indexed")
	}
	if diffContainsLineRange(diff, "path with space.go", 5, 12) {
		t.Fatal("range crossing absent lines unexpectedly matched")
	}
}

func TestDiffLineIndexUsesOldSideForDeletedFile(t *testing.T) {
	diff := "--- a/deleted.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-old\n"
	if !diffContainsLineRange(diff, "deleted.go", 1, 1) {
		t.Fatal("deleted source line was not indexed")
	}
}

func TestDiffLineIndexDoesNotTrustTruncatedHunkCount(t *testing.T) {
	diff := "--- a/main.go\n+++ b/main.go\n@@ -1,100 +1,100 @@\n first\n+second\n[ZEPHYR TRUNCATED]\n"
	if !diffContainsLineRange(diff, "main.go", 1, 2) {
		t.Fatal("present immutable lines were not indexed")
	}
	if diffContainsLineRange(diff, "main.go", 3, 100) {
		t.Fatal("declared but absent hunk lines unexpectedly matched")
	}
}
