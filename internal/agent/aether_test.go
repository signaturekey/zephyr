package agent

import (
	"strings"
	"testing"

	"github.com/signaturekey/zephyr/internal/snapshot"
	"github.com/stretchr/testify/assert"
)

func TestFilterDiffReturnsOnlyRolePrimaryPaths(t *testing.T) {
	diff := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n" +
		"diff --git a/web.ts b/web.ts\n--- a/web.ts\n+++ b/web.ts\n@@ -1 +1 @@\n-old\n+new\n"
	filtered := filterDiff(diff, []string{"main.go"})
	assert.Contains(t, filtered, "b/main.go")
	assert.False(t, strings.Contains(filtered, "b/web.ts"))
}

func TestPacketTextExposesOnlyFrozenSnapshotPath(t *testing.T) {
	snap := &snapshot.Snapshot{
		Root:       "/tmp/zephyr-review-frozen",
		Repository: "/private/live-checkout",
		Source:     snapshot.SourceWorktree,
	}

	packet := packetText(snap, nil, "")

	assert.Contains(t, packet, `"repository": "reviewed-repository"`)
	assert.Contains(t, packet, `"snapshot_root": "/tmp/zephyr-review-frozen"`)
	assert.NotContains(t, packet, "/private/live-checkout")
}
