package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/signaturekey/zephyr/internal/codexharness/diagnostics"
	"github.com/signaturekey/zephyr/internal/codexharness/process"
)

type Result struct {
	OutputPath string
	Category   diagnostics.Category
	Attempts   int
}
type Common struct{ PolicyPath, CompatibilityPath, OutputPath, PrivateDiagnosticsDir string }
type ProbeRequest struct{ PolicyPath, OutputPath, PrivateDiagnosticsDir string }
type RoutingRequest struct {
	Common
	PacketPath, RequestPath string
}
type ReviewerRequest struct {
	Common
	Role, PacketPath string
	FormatRetry      bool
}
type EvidenceRequest struct {
	Common
	PrecheckedPath, EvidencePath string
}
type Client interface {
	Probe(context.Context, ProbeRequest) (Result, error)
	Smoke(context.Context, Common) (Result, error)
	Route(context.Context, RoutingRequest) (Result, error)
	Review(context.Context, ReviewerRequest) (Result, error)
	Evidence(context.Context, EvidenceRequest) (Result, error)
}

type ScriptClient struct {
	path   string
	runner process.Runner
	Env    []string
}

func New(path string, runner process.Runner) *ScriptClient {
	if runner == nil {
		runner = process.ExecRunner{}
	}
	return &ScriptClient{path: path, runner: runner}
}

func (c *ScriptClient) Probe(ctx context.Context, r ProbeRequest) (Result, error) {
	args := []string{"probe", "--output", r.OutputPath}
	if r.PolicyPath != "" {
		args = append(args, "--policy", r.PolicyPath)
	}
	args = appendPrivate(args, r.PrivateDiagnosticsDir)
	return c.call(ctx, "probe", "", r.OutputPath, args, r.PolicyPath)
}
func (c *ScriptClient) Smoke(ctx context.Context, r Common) (Result, error) {
	args := []string{"smoke", "--compat", r.CompatibilityPath, "--output", r.OutputPath}
	if r.PolicyPath != "" {
		args = append(args, "--policy", r.PolicyPath)
	}
	args = appendPrivate(args, r.PrivateDiagnosticsDir)
	return c.call(ctx, "smoke", "", r.OutputPath, args, r.PolicyPath, r.CompatibilityPath)
}
func (c *ScriptClient) Route(ctx context.Context, r RoutingRequest) (Result, error) {
	args := []string{"routing", "--packet", r.PacketPath, "--request", r.RequestPath, "--compat", r.CompatibilityPath, "--output", r.OutputPath}
	if r.PolicyPath != "" {
		args = append(args, "--policy", r.PolicyPath)
	}
	args = appendPrivate(args, r.PrivateDiagnosticsDir)
	return c.call(ctx, "routing", "semantic-router", r.OutputPath, args, r.PolicyPath, r.CompatibilityPath, r.PacketPath, r.RequestPath)
}
func (c *ScriptClient) Review(ctx context.Context, r ReviewerRequest) (Result, error) {
	if !validRole(r.Role) {
		return Result{}, errors.New("unknown reviewer role")
	}
	args := []string{"reviewer", "--role", r.Role, "--packet", r.PacketPath, "--compat", r.CompatibilityPath, "--output", r.OutputPath}
	if r.PolicyPath != "" {
		args = append(args, "--policy", r.PolicyPath)
	}
	if r.FormatRetry {
		args = append(args, "--format-retry")
	}
	args = appendPrivate(args, r.PrivateDiagnosticsDir)
	return c.call(ctx, "reviewer", r.Role, r.OutputPath, args, r.PolicyPath, r.CompatibilityPath, r.PacketPath)
}
func (c *ScriptClient) Evidence(ctx context.Context, r EvidenceRequest) (Result, error) {
	args := []string{"evidence", "--prechecked", r.PrecheckedPath, "--evidence", r.EvidencePath, "--compat", r.CompatibilityPath, "--output", r.OutputPath}
	if r.PolicyPath != "" {
		args = append(args, "--policy", r.PolicyPath)
	}
	args = appendPrivate(args, r.PrivateDiagnosticsDir)
	return c.call(ctx, "evidence", "evidence-gate", r.OutputPath, args, r.PolicyPath, r.CompatibilityPath, r.PrecheckedPath, r.EvidencePath)
}
func appendPrivate(args []string, dir string) []string {
	if dir != "" {
		return append(args, "--private-diagnostics-dir", dir)
	}
	return args
}

func (c *ScriptClient) call(ctx context.Context, kind, role, output string, args []string, inputs ...string) (Result, error) {
	if err := regularExecutable(c.path); err != nil {
		return Result{}, err
	}
	if err := newOutput(output); err != nil {
		return Result{}, err
	}
	for _, input := range inputs {
		if input != "" {
			if err := regularInput(input); err != nil {
				return Result{}, err
			}
		}
	}
	result, err := c.runner.Run(ctx, process.Request{Path: c.path, Args: args, Env: append([]string(nil), c.Env...), OutputLimit: 64 << 10})
	if err != nil {
		return Result{}, fmt.Errorf("run dispatcher: %w", err)
	}
	category, attempts := parseDiagnostic(result.Stderr)
	if result.TimedOut {
		category = diagnostics.CategoryTimeout
	}
	if result.ExitCode != 0 {
		return Result{OutputPath: output, Category: category, Attempts: attempts}, &Error{Category: category, Attempts: attempts, ExitCode: result.ExitCode}
	}
	var success struct{ Kind, Role, Output string }
	if err := json.Unmarshal(result.Stdout, &success); err != nil || success.Kind != kind || success.Role != role || success.Output != output {
		return Result{}, errors.New("dispatcher returned invalid success JSON")
	}
	if err := regularInput(output); err != nil {
		return Result{}, fmt.Errorf("dispatcher output: %w", err)
	}
	return Result{OutputPath: output, Category: category, Attempts: attempts}, nil
}

type Error struct {
	Category           diagnostics.Category
	Attempts, ExitCode int
}

func (e *Error) Error() string {
	return fmt.Sprintf("dispatcher failed: category=%s exit=%d", e.Category, e.ExitCode)
}

var diagnostic = regexp.MustCompile(`(?:^|[[:space:]])category=(auth|config|sandbox|rate-limit|provider-unavailable|transport|timeout|validation|lifecycle|unknown)(?:[[:space:]]|$)`)

func parseDiagnostic(stderr []byte) (diagnostics.Category, int) {
	m := diagnostic.FindStringSubmatch(string(stderr))
	c := diagnostics.CategoryUnknown
	if len(m) == 2 {
		c = diagnostics.Category(m[1])
	}
	a := 1
	if strings.Contains(string(stderr), "after 2 attempts") {
		a = 2
	}
	return c, a
}
func regularExecutable(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("dispatcher path must be absolute")
	}
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0o111 == 0 {
		return errors.New("dispatcher must be an executable regular file")
	}
	return nil
}
func regularInput(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("input path must be absolute")
	}
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() || i.Mode()&os.ModeSymlink != 0 {
		return errors.New("input must be a regular non-symlink file")
	}
	return nil
}
func newOutput(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("output path must be absolute")
	}
	if _, e := os.Lstat(path); e == nil {
		return errors.New("output path already exists")
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	i, e := os.Lstat(filepath.Dir(path))
	if e != nil {
		return e
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return errors.New("output parent must be a non-symlink directory")
	}
	return nil
}
func validRole(role string) bool {
	switch role {
	case "code-reviewer", "architect-reviewer", "golang-expert", "python-expert", "typescript-expert", "react-expert", "frontend-expert", "skill-authoring-expert", "reliability-expert", "messaging-expert", "infrastructure-expert", "storage-expert", "security-auditor", "sql-expert", "contract-reviewer", "qa-expert", "code-simplifier":
		return true
	}
	return false
}
