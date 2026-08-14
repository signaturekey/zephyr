package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/signaturekey/zephyr/internal/codexharness/review"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeApplication struct {
	doctor review.DoctorResult
	review review.Result
}

func TestDoctorOperationFactoryHonorsPrivateDiagnostics(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	factory := doctorOperationFactory(filepath.Join(base, "driver"), true)

	operation, err := factory()

	require.NoError(t, err)
	assert.NotEmpty(t, operation.PrivateDir)
	assert.DirExists(t, operation.PrivateDir)
}

func (fake fakeApplication) Doctor(context.Context, bool) review.DoctorResult { return fake.doctor }
func (fake fakeApplication) Review(context.Context, review.ReviewOptions) (review.Result, error) {
	return fake.review, nil
}

func TestRunMain_DoctorWritesOneJSONDocument(t *testing.T) {
	app := fakeApplication{doctor: review.DoctorResult{OK: true, Checks: []review.DoctorCheck{}, DiagnosticsPath: "/tmp/doctor.json"}}
	var stdout, stderr bytes.Buffer

	exitCode := runMain([]string{"doctor"}, nil, &stdout, &stderr, func() (application, error) { return app, nil })

	assert.Equal(t, 0, exitCode)
	var document review.DoctorResult
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &document))
	assert.True(t, document.OK)
	assert.Contains(t, stderr.String(), "doctor")
	assert.NotContains(t, stdout.String(), "progress")
}

func TestRunMain_ReviewRequiresAbsoluteRepository(t *testing.T) {
	var stdout, stderr bytes.Buffer

	exitCode := runMain([]string{"review", "--repo", "relative"}, nil, &stdout, &stderr, func() (application, error) { return fakeApplication{}, nil })

	assert.NotEqual(t, 0, exitCode)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "absolute")
}

func TestRunMain_PrivateDiagnosticsWarningOnlyUsesStderr(t *testing.T) {
	app := fakeApplication{doctor: review.DoctorResult{OK: true, Checks: []review.DoctorCheck{}, DiagnosticsPath: "/tmp/doctor.json"}}
	var stdout, stderr bytes.Buffer

	exitCode := runMain([]string{"doctor", "--keep-private-diagnostics"}, nil, &stdout, &stderr, func() (application, error) { return app, nil })

	assert.Equal(t, 0, exitCode)
	assert.NotContains(t, stdout.String(), "proprietary")
	assert.Contains(t, stderr.String(), "may contain proprietary code/model output")
}
