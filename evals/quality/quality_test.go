package quality

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareDirectoriesDetectsReviewerQualityRegression(t *testing.T) {
	baseline := t.TempDir()
	candidate := t.TempDir()
	writeRecord(t, baseline, `{
  "case_id":"case-one",
  "baseline":{"human_findings":[{"id":"human-one","severity":"P1"},{"id":"human-two","severity":"P1"}]},
  "zephyr_run":{"findings":[{"id":"old-one","severity":"P1"},{"id":"old-two","severity":"P1"}]},
  "comparison":{"matched":[{"human_id":"human-one","zephyr_ids":["old-one"]},{"human_id":"human-two","zephyr_ids":["old-two"]}],"missed_human_ids":[],"zephyr_only":[]}
}`)
	writeRecord(t, candidate, `{
  "case_id":"case-one",
  "baseline":{"human_findings":[{"id":"human-one","severity":"P1"},{"id":"human-two","severity":"P1"}]},
  "zephyr_run":{"findings":[{"id":"new-two","severity":"P2"},{"id":"new-extra","severity":"P2"}]},
  "comparison":{"matched":[{"human_id":"human-two","zephyr_ids":["new-two"]}],"missed_human_ids":["human-one"],"zephyr_only":[{"zephyr_id":"new-extra","disposition":"false-positive"}]}
}`)

	comparison, err := CompareDirectories(baseline, candidate)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"confirmed-finding recall decreased",
		"false-positive rate increased",
		"P0-P3 severity agreement decreased",
	}, comparison.Regressions)
}

func TestCompareDirectoriesAcceptsEqualQuality(t *testing.T) {
	baseline := t.TempDir()
	candidate := t.TempDir()
	record := `{
  "case_id":"case-one",
  "baseline":{"human_findings":[{"id":"human-one","severity":"P2"}]},
  "zephyr_run":{"findings":[{"id":"zephyr-one","severity":"P2"}]},
  "comparison":{"matched":[{"human_id":"human-one","zephyr_ids":["zephyr-one"]}],"missed_human_ids":[],"zephyr_only":[]}
}`
	writeRecord(t, baseline, record)
	writeRecord(t, candidate, record)

	comparison, err := CompareDirectories(baseline, candidate)
	require.NoError(t, err)
	assert.Empty(t, comparison.Regressions)
	assert.Equal(t, 1.0, comparison.Candidate.Recall())
	assert.Equal(t, 1.0, comparison.Candidate.SeverityAgreement())
}

func writeRecord(t *testing.T, dir, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "case.json"), []byte(content), 0o600))
}
