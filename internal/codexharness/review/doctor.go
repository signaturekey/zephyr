package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/signaturekey/zephyr/internal/codexharness/budget"
	"github.com/signaturekey/zephyr/internal/codexharness/compatibility"
	"github.com/signaturekey/zephyr/internal/codexharness/diagnostics"
	"github.com/signaturekey/zephyr/internal/codexharness/preflight"
)

type DoctorResult struct {
	OK              bool          `json:"ok"`
	Checks          []DoctorCheck `json:"checks"`
	DiagnosticsPath string        `json:"diagnostics_path"`
}

type DoctorCheck struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	ReasonCode  string `json:"reason_code,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

type DoctorPreflight interface {
	Check(context.Context) (preflight.Result, error)
}

type DoctorCompatibility interface {
	Ensure(context.Context, string, string, string) (compatibility.Result, error)
}

type DoctorDependencies struct {
	Preflight      DoctorPreflight
	Compatibility  DoctorCompatibility
	BeginOperation func() (diagnostics.Operation, error)
	WritePolicy    func(string) (string, error)
	Timeout        time.Duration
}

type Doctor struct{ dependencies DoctorDependencies }

func NewDoctor(dependencies DoctorDependencies) *Doctor { return &Doctor{dependencies: dependencies} }

func (doctor *Doctor) Run(parent context.Context) DoctorResult {
	result := DoctorResult{Checks: make([]DoctorCheck, 0, 4)}
	if doctor == nil || doctor.dependencies.BeginOperation == nil {
		return appendDoctorCheck(result, failedCheck("operation", "configuration-invalid"))
	}
	operation, err := doctor.dependencies.BeginOperation()
	if err != nil {
		return appendDoctorCheck(result, failedCheck("operation", "operation-create-failed"))
	}
	result.DiagnosticsPath = operation.DiagnosticsPath
	limit := doctor.dependencies.Timeout
	if limit <= 0 {
		limit = budget.DoctorTotal
	}
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()

	if doctor.dependencies.Preflight == nil {
		result.Checks = append(result.Checks, failedCheck("preflight", "configuration-invalid"))
		return doctor.finish(operation, result)
	}
	preflightResult, err := doctor.dependencies.Preflight.Check(ctx)
	if err != nil {
		result.Checks = append(result.Checks, failedCheck("preflight", "preflight-failed"))
		return doctor.finish(operation, result)
	}
	result.Checks = append(result.Checks, DoctorCheck{Name: "preflight", Status: "ok"})
	result.Checks = append(result.Checks, authCheck(preflightResult.AuthFile))
	result.Checks = append(result.Checks, loginCheck(preflightResult.LoginState))
	if doctor.dependencies.WritePolicy == nil || doctor.dependencies.Compatibility == nil {
		result.Checks = append(result.Checks, failedCheck("compatibility", "configuration-invalid"))
		return doctor.finish(operation, result)
	}
	policy, err := doctor.dependencies.WritePolicy(operation.Root)
	if err != nil {
		result.Checks = append(result.Checks, failedCheck("compatibility", "doctor-policy-failed"))
		return doctor.finish(operation, result)
	}
	compatibilityResult, err := doctor.dependencies.Compatibility.Ensure(ctx, policy, operation.OutputsDir, operation.PrivateDir)
	if err != nil {
		result.Checks = append(result.Checks, failedCheck("compatibility", "compatibility-failed"))
		return doctor.finish(operation, result)
	}
	reason := "cache-miss"
	if compatibilityResult.CacheHit {
		reason = "cache-hit"
	}
	result.Checks = append(result.Checks, DoctorCheck{Name: "compatibility", Status: "ok", ReasonCode: reason})
	result.OK = true
	return doctor.finish(operation, result)
}

func (doctor *Doctor) finish(operation diagnostics.Operation, result DoctorResult) DoctorResult {
	if result.DiagnosticsPath == "" {
		result.DiagnosticsPath = operation.DiagnosticsPath
	}
	if err := writeDoctorDiagnostics(operation.DiagnosticsPath, result); err != nil {
		result.OK = false
		result.Checks = append(result.Checks, failedCheck("diagnostics", "diagnostics-write-failed"))
	}
	return result
}

func appendDoctorCheck(result DoctorResult, check DoctorCheck) DoctorResult {
	result.Checks = append(result.Checks, check)
	return result
}

func failedCheck(name, reason string) DoctorCheck {
	return DoctorCheck{Name: name, Status: "failed", ReasonCode: reason}
}

func authCheck(status preflight.AuthFileStatus) DoctorCheck {
	switch status {
	case preflight.AuthRegular:
		return DoctorCheck{Name: "auth-file", Status: "ok"}
	case preflight.AuthMissing:
		return DoctorCheck{Name: "auth-file", Status: "warning", ReasonCode: "auth-file-missing", Remediation: "Codex stores credentials in an auth file; run codex login to create a fresh private credential file."}
	case preflight.AuthSymlink:
		return DoctorCheck{Name: "auth-file", Status: "failed", ReasonCode: "auth-file-symlink", Remediation: "Replace the symlink with a private regular Codex auth file, then run codex login."}
	case preflight.AuthWrongOwner:
		return DoctorCheck{Name: "auth-file", Status: "failed", ReasonCode: "auth-file-wrong-owner", Remediation: "Use a private auth file owned by the current user, then run codex login."}
	case preflight.AuthUnsafeMode:
		return DoctorCheck{Name: "auth-file", Status: "failed", ReasonCode: "auth-file-unsafe-mode", Remediation: "Restrict the Codex auth file to the current user, then run codex login."}
	default:
		return DoctorCheck{Name: "auth-file", Status: "warning", ReasonCode: "auth-file-unknown", Remediation: "Run codex login to refresh file-based Codex credentials."}
	}
}

func loginCheck(state preflight.LoginState) DoctorCheck {
	if state == preflight.LoginAuthenticated {
		return DoctorCheck{Name: "login", Status: "ok"}
	}
	if state == preflight.LoginRequired {
		return DoctorCheck{Name: "login", Status: "warning", ReasonCode: "login-required", Remediation: "Run codex login, then run zephyr-codex doctor again."}
	}
	return DoctorCheck{Name: "login", Status: "warning", ReasonCode: "login-status-unknown"}
}

func writeDoctorDiagnostics(path string, result DoctorResult) error {
	if !filepath.IsAbs(path) {
		return errors.New("diagnostics path must be absolute")
	}
	data, err := json.MarshalIndent(struct {
		Version int           `json:"version"`
		Kind    string        `json:"kind"`
		OK      bool          `json:"ok"`
		Checks  []DoctorCheck `json:"checks"`
	}{Version: 1, Kind: "zephyr-codex-doctor", OK: result.OK, Checks: result.Checks}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode doctor diagnostics: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".doctor-diagnostics-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
