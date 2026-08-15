package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"version"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.True(t, strings.HasPrefix(stdout.String(), "zephyr "))
	assert.Empty(t, stderr.String())
}

func TestReviewRejectsConflictingSelectorsBeforeStartingRuntime(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"review", "--worktree", "--commit", "HEAD"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "choose exactly one")
}

func TestReviewBranchRequiresBase(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"review", "--branch", "feature"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--branch requires --base")
}
