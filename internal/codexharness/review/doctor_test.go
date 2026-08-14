package review

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/signaturekey/zephyr/internal/codexharness/compatibility"
	"github.com/signaturekey/zephyr/internal/codexharness/diagnostics"
	"github.com/signaturekey/zephyr/internal/codexharness/preflight"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type doctorPreflightFake struct {
	result preflight.Result
	err    error
}

func (fake doctorPreflightFake) Check(context.Context) (preflight.Result, error) {
	return fake.result, fake.err
}

type doctorCompatibilityFake struct {
	policy, operationDir, privateDir string
	result                           compatibility.Result
	err                              error
}

func (fake *doctorCompatibilityFake) Ensure(_ context.Context, policy, operationDir, privateDir string) (compatibility.Result, error) {
	fake.policy, fake.operationDir, fake.privateDir = policy, operationDir, privateDir
	return fake.result, fake.err
}

func TestDoctor_SmokeSuccessUsesIsolatedPolicy(t *testing.T) {
	operation := doctorOperation(t)
	compatibilityCheck := &doctorCompatibilityFake{result: compatibility.Result{CacheHit: true, LiveSmokeDone: true}}
	preflightCheck := doctorPreflightFake{result: preflight.Result{
		ZephyrPath: "/usr/local/bin/zephyr", CodexPath: "/usr/local/bin/codex", DispatcherPath: "/opt/dispatch.sh",
		SessionPrimitive: "setsid", CoreVersion: preflight.CoreVersion{Version: "1.2.3"}, CodexVersion: "0.91.0",
		LoginState: preflight.LoginAuthenticated, AuthFile: preflight.AuthRegular,
	}}
	doctor := NewDoctor(DoctorDependencies{
		Preflight: preflightCheck, Compatibility: compatibilityCheck,
		BeginOperation: func() (diagnostics.Operation, error) { return operation, nil },
		WritePolicy:    compatibility.WriteDoctorPolicy,
	})

	result := doctor.Run(t.Context())

	assert.True(t, result.OK)
	assert.Equal(t, operation.DiagnosticsPath, result.DiagnosticsPath)
	assert.Equal(t, filepath.Join(operation.Root, "doctor-model-policy.tsv"), compatibilityCheck.policy)
	assert.Equal(t, operation.OutputsDir, compatibilityCheck.operationDir)
	assert.Empty(t, compatibilityCheck.privateDir)
	assertCheck(t, result.Checks, "preflight", "ok", "")
	assertCheck(t, result.Checks, "compatibility", "ok", "cache-hit")
}

func TestDoctor_MissingAuthIsContextOnlyWhenSmokeSucceeds(t *testing.T) {
	operation := doctorOperation(t)
	doctor := NewDoctor(DoctorDependencies{
		Preflight: doctorPreflightFake{result: preflight.Result{
			ZephyrPath: "/usr/local/bin/zephyr", CodexPath: "/usr/local/bin/codex", DispatcherPath: "/opt/dispatch.sh",
			SessionPrimitive: "setsid", CoreVersion: preflight.CoreVersion{Version: "1.2.3"}, CodexVersion: "0.91.0",
			LoginState: preflight.LoginUnknown, AuthFile: preflight.AuthMissing,
		}},
		Compatibility:  &doctorCompatibilityFake{result: compatibility.Result{LiveSmokeDone: true}},
		BeginOperation: func() (diagnostics.Operation, error) { return operation, nil },
		WritePolicy:    compatibility.WriteDoctorPolicy,
	})

	result := doctor.Run(t.Context())

	assert.True(t, result.OK)
	assertCheck(t, result.Checks, "auth-file", "warning", "auth-file-missing")
	assertCheck(t, result.Checks, "compatibility", "ok", "cache-miss")
}

func TestDoctor_CompatibilityFailureIsNotMaskedByAuthFile(t *testing.T) {
	operation := doctorOperation(t)
	doctor := NewDoctor(DoctorDependencies{
		Preflight: doctorPreflightFake{result: preflight.Result{
			ZephyrPath: "/usr/local/bin/zephyr", CodexPath: "/usr/local/bin/codex", DispatcherPath: "/opt/dispatch.sh",
			SessionPrimitive: "setsid", CoreVersion: preflight.CoreVersion{Version: "1.2.3"}, CodexVersion: "0.91.0",
			LoginState: preflight.LoginRequired, AuthFile: preflight.AuthMissing,
		}},
		Compatibility:  &doctorCompatibilityFake{err: assert.AnError},
		BeginOperation: func() (diagnostics.Operation, error) { return operation, nil },
		WritePolicy:    compatibility.WriteDoctorPolicy,
	})

	result := doctor.Run(t.Context())

	assert.False(t, result.OK)
	assertCheck(t, result.Checks, "compatibility", "failed", "compatibility-failed")
	assertCheck(t, result.Checks, "auth-file", "warning", "auth-file-missing")
}

func doctorOperation(t *testing.T) diagnostics.Operation {
	t.Helper()
	root := t.TempDir()
	return diagnostics.Operation{ID: "0123456789abcdef0123456789abcdef", Root: root, OutputsDir: filepath.Join(root, "outputs"), DiagnosticsPath: filepath.Join(root, "diagnostics.json")}
}

func assertCheck(t *testing.T, checks []DoctorCheck, name, status, reason string) {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			assert.Equal(t, status, check.Status)
			assert.Equal(t, reason, check.ReasonCode)
			return
		}
	}
	require.Failf(t, "doctor check is missing", "name=%q checks=%#v", name, checks)
}
