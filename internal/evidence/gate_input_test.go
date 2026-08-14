package evidence

import (
	"testing"

	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildGateInputExtractsOnlyExactOverlappingFrozenHunks(t *testing.T) {
	const firstHunk = "--- a/alpha.go\n+++ b/alpha.go\n@@ -1,2 +1,3 @@\n package alpha\n+func added() {}\n func kept() {}\n"
	const secondHunk = "--- a/alpha.go\n+++ b/alpha.go\n@@ -10 +11 @@\n-old\n+new\n"
	const deletedHunk = "--- a/deleted.go\n+++ /dev/null\n@@ -4,2 +0,0 @@\n-oldOne\n-oldTwo\n"
	const quotedHunk = "--- \"a/path with space.go\"\n+++ \"b/path with space.go\"\n@@ -3 +3 @@\n-old\n+new\n"
	diff := "diff --git a/alpha.go b/alpha.go\nindex 111..222 100644\n" + firstHunk + "\n" + secondHunk +
		"diff --git a/deleted.go b/deleted.go\ndeleted file mode 100644\n" + deletedHunk +
		"diff --git \"a/path with space.go\" \"b/path with space.go\"\n" + quotedHunk +
		"diff --git a/unrelated.go b/unrelated.go\n--- a/unrelated.go\n+++ b/unrelated.go\n@@ -1 +1 @@\n-old\n+new\n"

	input, err := BuildGateInput(CandidateSet{
		Version: schema.ProtocolVersion,
		RunID:   "run-1",
		Findings: []schema.CandidateFinding{
			gateCandidate("z-one", "alpha.go", 2, 2),
			gateCandidate("a-two", "alpha.go", 11, 11),
			gateCandidate("b-deleted", "deleted.go", 4, 5),
			gateCandidate("c-quoted", "path with space.go", 3, 3),
		},
	}, contextpack.Packet{Version: contextpack.Version, RunID: "run-1", Diff: contextpack.Diff{Full: diff}})
	require.NoError(t, err)

	assert.Equal(t, GateInput{
		Version: schema.ProtocolVersion,
		RunID:   "run-1",
		Items: []GateEvidenceItem{
			{CandidateID: "a-two", Location: schema.FindingLocation{File: "alpha.go", LineStart: 11, LineEnd: 11}, DiffHunks: []string{secondHunk}},
			{CandidateID: "b-deleted", Location: schema.FindingLocation{File: "deleted.go", LineStart: 4, LineEnd: 5}, DiffHunks: []string{deletedHunk}},
			{CandidateID: "c-quoted", Location: schema.FindingLocation{File: "path with space.go", LineStart: 3, LineEnd: 3}, DiffHunks: []string{quotedHunk}},
			{CandidateID: "z-one", Location: schema.FindingLocation{File: "alpha.go", LineStart: 2, LineEnd: 2}, DiffHunks: []string{firstHunk}},
		},
	}, input)
}

func TestBuildGateInputSharesHunksAndPreservesSourceOrder(t *testing.T) {
	const firstHunk = "--- a/main.go\n+++ b/main.go\n@@ -1,2 +1,2 @@\n-old\n+new\n kept\n"
	const secondHunk = "--- a/main.go\n+++ b/main.go\n@@ -10,2 +10,2 @@\n-oldTwo\n+newTwo\n keptTwo\n"
	diff := "diff --git a/main.go b/main.go\n" + firstHunk + "\n" + secondHunk

	input, err := BuildGateInput(CandidateSet{
		Version: schema.ProtocolVersion,
		RunID:   "run-1",
		Findings: []schema.CandidateFinding{
			gateCandidate("same-a", "main.go", 1, 1),
			gateCandidate("same-b", "main.go", 2, 2),
			gateCandidate("both", "main.go", 2, 10),
		},
	}, contextpack.Packet{Version: contextpack.Version, RunID: "run-1", Diff: contextpack.Diff{Full: diff}})
	require.NoError(t, err)

	assert.Equal(t, []string{firstHunk, secondHunk}, input.Items[0].DiffHunks)
	assert.Equal(t, []string{firstHunk}, input.Items[1].DiffHunks)
	assert.Equal(t, []string{firstHunk}, input.Items[2].DiffHunks)
}

func TestBuildGateInputNormalizesMissingLineEnd(t *testing.T) {
	const hunk = "--- a/main.go\n+++ b/main.go\n@@ -184 +184 @@\n-old\n+new\n"
	candidate := gateCandidate("one", "main.go", 184, 0)

	input, err := BuildGateInput(CandidateSet{
		Version:  schema.ProtocolVersion,
		RunID:    "run-1",
		Findings: []schema.CandidateFinding{candidate},
	}, contextpack.Packet{Version: contextpack.Version, RunID: "run-1", Diff: contextpack.Diff{Full: hunk}})

	require.NoError(t, err)
	require.Equal(t, schema.FindingLocation{File: "main.go", LineStart: 184, LineEnd: 184}, input.Items[0].Location)
}

func TestBuildGateInputRejectsInvalidOrUnmappedCandidates(t *testing.T) {
	packet := contextpack.Packet{Version: contextpack.Version, RunID: "run-1", Diff: contextpack.Diff{Full: "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n"}}
	valid := CandidateSet{Version: schema.ProtocolVersion, RunID: "run-1", Findings: []schema.CandidateFinding{gateCandidate("one", "main.go", 1, 1)}}

	for name, candidates := range map[string]CandidateSet{
		"version mismatch":  {Version: 99, RunID: "run-1"},
		"run mismatch":      {Version: schema.ProtocolVersion, RunID: "other"},
		"empty id":          {Version: schema.ProtocolVersion, RunID: "run-1", Findings: []schema.CandidateFinding{gateCandidate("", "main.go", 1, 1)}},
		"artifact location": {Version: schema.ProtocolVersion, RunID: "run-1", Findings: []schema.CandidateFinding{{ID: "artifact", Location: schema.FindingLocation{Artifact: "PLAN.md", Section: "scope"}}}},
		"unmapped range":    {Version: schema.ProtocolVersion, RunID: "run-1", Findings: []schema.CandidateFinding{gateCandidate("missing", "main.go", 99, 99)}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := BuildGateInput(candidates, packet)
			assert.Error(t, err)
		})
	}

	packet.RunID = "other"
	_, err := BuildGateInput(valid, packet)
	assert.Error(t, err)
}

func TestBuildGateInputKeepsEmptyValidCandidateSetMinimal(t *testing.T) {
	input, err := BuildGateInput(CandidateSet{Version: schema.ProtocolVersion, RunID: "run-1", Findings: []schema.CandidateFinding{}}, contextpack.Packet{
		Version: contextpack.Version,
		RunID:   "run-1",
		Plan:    &contextpack.Document{Content: "MUST NOT LEAK"},
		Diff:    contextpack.Diff{Full: "this is not a parseable diff"},
	})
	require.NoError(t, err)
	assert.Equal(t, GateInput{Version: schema.ProtocolVersion, RunID: "run-1", Items: []GateEvidenceItem{}}, input)
}

func TestBuildGateInputDoesNotTreatAddedTriplePlusLineAsFileHeader(t *testing.T) {
	const laterHunk = "--- a/main.go\n+++ b/main.go\n@@ -10 +11 @@\n-oldLater\n+newLater\n"
	diff := "--- a/main.go\n+++ b/main.go\n@@ -1 +1,2 @@\n old\n+++ addedContent\n\n" +
		"@@ -10 +11 @@\n-oldLater\n+newLater\n"

	input, err := BuildGateInput(CandidateSet{
		Version:  schema.ProtocolVersion,
		RunID:    "run-1",
		Findings: []schema.CandidateFinding{gateCandidate("later", "main.go", 11, 11)},
	}, contextpack.Packet{Version: contextpack.Version, RunID: "run-1", Diff: contextpack.Diff{Full: diff}})
	require.NoError(t, err)
	assert.Equal(t, []string{laterHunk}, input.Items[0].DiffHunks)
}

func gateCandidate(id, path string, start, end int) schema.CandidateFinding {
	return schema.CandidateFinding{ID: id, Location: schema.FindingLocation{File: path, LineStart: start, LineEnd: end}}
}
