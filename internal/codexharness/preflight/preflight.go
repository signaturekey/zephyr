package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/signaturekey/zephyr/internal/codexharness/process"
	"github.com/signaturekey/zephyr/internal/codexharness/trust"
	"github.com/signaturekey/zephyr/internal/protocol"
	"github.com/signaturekey/zephyr/internal/schema"
)

type Result struct {
	ZephyrPath       string
	ZephyrSHA256     string
	CodexPath        string
	CodexSHA256      string
	DispatcherPath   string
	SessionPrimitive string
	CoreVersion      CoreVersion
	CodexVersion     string
	LoginState       LoginState
	AuthFile         AuthFileStatus
}

type executableIdentity struct {
	Path   string
	SHA256 string
}
type CoreVersion struct {
	Version                string `json:"version"`
	Commit                 string `json:"commit"`
	Dirty                  string `json:"dirty"`
	ProtocolVersion        int    `json:"protocol_version"`
	CodexHarnessAPIVersion int    `json:"codex_harness_api_version"`
}
type LoginState string

const (
	LoginAuthenticated LoginState = "authenticated"
	LoginRequired      LoginState = "login-required"
	LoginUnknown       LoginState = "unknown"
)

type AuthFileStatus string

const (
	AuthRegular    AuthFileStatus = "regular"
	AuthMissing    AuthFileStatus = "missing"
	AuthSymlink    AuthFileStatus = "symlink"
	AuthWrongOwner AuthFileStatus = "wrong-owner"
	AuthUnsafeMode AuthFileStatus = "unsafe-mode"
)

type Options struct {
	ZephyrPath, CodexPath, DispatcherPath, CodexHome, Home string
	Runner                                                 process.Runner
	LookupPath                                             func(string) (string, error)
	CoreEnv                                                []string
}
type Checker struct{ options Options }

func New(options Options) *Checker                           { return &Checker{options: options} }
func (c *Checker) Check(ctx context.Context) (Result, error) { return Check(ctx, c.options) }
func Check(ctx context.Context, options Options) (Result, error) {
	runner := options.Runner
	if runner == nil {
		runner = process.ExecRunner{}
	}
	lookup := options.LookupPath
	if lookup == nil {
		lookup = exec.LookPath
	}
	zephyr, err := resolveExecutableIdentity(options.ZephyrPath, "zephyr", lookup)
	if err != nil {
		return Result{}, err
	}
	codex, err := resolveExecutableIdentity(options.CodexPath, "codex", lookup)
	if err != nil {
		return Result{}, err
	}
	dispatcher := options.DispatcherPath
	if dispatcher == "" {
		home := options.Home
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		dispatcher = filepath.Join(home, ".agents", "skills", "zephyr", "scripts", "dispatch.sh")
	}
	if !filepath.IsAbs(dispatcher) {
		return Result{}, errors.New("dispatcher path must be absolute")
	}
	if _, err := os.Lstat(dispatcher); err != nil {
		return Result{}, fmt.Errorf("inspect dispatcher: %w", err)
	}
	primitive, err := sessionPrimitive(lookup)
	if err != nil {
		return Result{}, err
	}
	if len(options.CoreEnv) == 0 {
		return Result{}, errors.New("preflight core environment is required")
	}
	core, err := coreVersion(ctx, runner, zephyr.Path, options.CoreEnv)
	if err != nil {
		return Result{}, err
	}
	if core.ProtocolVersion != schema.ProtocolVersion || core.CodexHarnessAPIVersion != protocol.CodexHarnessAPIVersion {
		return Result{}, errors.New("Zephyr/Codex harness protocol mismatch")
	}
	codexVersion, err := codexVersion(ctx, runner, codex.Path, options.CoreEnv)
	if err != nil {
		return Result{}, err
	}
	login := loginState(ctx, runner, codex.Path, options.CoreEnv)
	if err := verifyExecutableIdentity(zephyr, "zephyr"); err != nil {
		return Result{}, err
	}
	if err := verifyExecutableIdentity(codex, "codex"); err != nil {
		return Result{}, err
	}
	home := options.CodexHome
	if home == "" {
		home = os.Getenv("CODEX_HOME")
	}
	if home == "" {
		base := options.Home
		if base == "" {
			base, _ = os.UserHomeDir()
		}
		home = filepath.Join(base, ".codex")
	}
	auth := authFileStatus(filepath.Join(home, "auth.json"))
	if auth == AuthWrongOwner || auth == AuthUnsafeMode || auth == AuthSymlink {
		return Result{}, fmt.Errorf("unsafe Codex auth file: %s", auth)
	}
	return Result{ZephyrPath: zephyr.Path, ZephyrSHA256: zephyr.SHA256, CodexPath: codex.Path, CodexSHA256: codex.SHA256, DispatcherPath: filepath.Clean(dispatcher), SessionPrimitive: primitive, CoreVersion: core, CodexVersion: codexVersion, LoginState: login, AuthFile: auth}, nil
}

func resolveExecutableIdentity(configured, name string, lookup func(string) (string, error)) (executableIdentity, error) {
	path, err := resolveExecutable(configured, name, lookup)
	if err != nil {
		return executableIdentity{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return executableIdentity{}, fmt.Errorf("inspect %s executable: %w", name, err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return executableIdentity{}, fmt.Errorf("%s executable must not be group or world writable", name)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return executableIdentity{}, fmt.Errorf("%s executable is not owned by the effective user", name)
	}
	digest, err := trust.HashRegular(path, false)
	if err != nil {
		return executableIdentity{}, fmt.Errorf("fingerprint %s executable: %w", name, err)
	}
	return executableIdentity{Path: path, SHA256: digest}, nil
}

func verifyExecutableIdentity(identity executableIdentity, name string) error {
	current, err := resolveExecutableIdentity(identity.Path, name, nil)
	if err != nil {
		return err
	}
	if current.Path != identity.Path || current.SHA256 != identity.SHA256 {
		return fmt.Errorf("%s executable changed after validation", name)
	}
	return nil
}

func resolveExecutable(configured, name string, lookup func(string) (string, error)) (string, error) {
	p := configured
	var err error
	if p == "" {
		if lookup == nil {
			return "", fmt.Errorf("locate %s: lookup is not configured", name)
		}
		p, err = lookup(name)
		if err != nil {
			return "", fmt.Errorf("locate %s: %w", name, err)
		}
	}
	if !filepath.IsAbs(p) {
		p, err = filepath.Abs(p)
		if err != nil {
			return "", err
		}
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("resolve %s executable: %w", name, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("%s is not a regular executable", name)
	}
	return filepath.Clean(resolved), nil
}
func ResolveExecutable(path string) (string, error) {
	return resolveExecutable(path, "executable", exec.LookPath)
}
func sessionPrimitive(lookup func(string) (string, error)) (string, error) {
	if p, e := lookup("setsid"); e == nil {
		if _, e = ResolveExecutable(p); e == nil {
			return "setsid", nil
		}
	}
	if p, e := lookup("perl"); e == nil {
		if _, e = ResolveExecutable(p); e == nil {
			return "perl", nil
		}
	}
	return "", errors.New("setsid or perl is required")
}
func coreVersion(ctx context.Context, runner process.Runner, path string, env []string) (CoreVersion, error) {
	r, e := runner.Run(ctx, process.Request{Path: path, Args: []string{"version"}, Env: append([]string(nil), env...), OutputLimit: 64 << 10})
	if e != nil {
		return CoreVersion{}, e
	}
	if r.ExitCode != 0 || r.TimedOut {
		return CoreVersion{}, errors.New("zephyr version failed")
	}
	var v CoreVersion
	if e = json.Unmarshal(r.Stdout, &v); e != nil {
		return CoreVersion{}, fmt.Errorf("decode zephyr version: %w", e)
	}
	if !safeVersion(v.Version) || !safeVersion(v.Commit) || !safeVersion(v.Dirty) {
		return CoreVersion{}, errors.New("unsafe Zephyr version output")
	}
	return v, nil
}
func codexVersion(ctx context.Context, runner process.Runner, path string, env []string) (string, error) {
	r, e := runner.Run(ctx, process.Request{Path: path, Args: []string{"--version"}, Env: append([]string(nil), env...), OutputLimit: 64 << 10})
	if e != nil {
		return "", e
	}
	s := strings.TrimSpace(string(r.Stdout))
	if r.ExitCode != 0 || r.TimedOut || !safeVersion(s) {
		return "", errors.New("codex version failed or is unsafe")
	}
	return s, nil
}
func loginState(ctx context.Context, runner process.Runner, path string, env []string) LoginState {
	r, e := runner.Run(ctx, process.Request{Path: path, Args: []string{"login", "status"}, Env: append([]string(nil), env...), OutputLimit: 64 << 10})
	if e != nil || r.TimedOut {
		return LoginUnknown
	}
	if r.ExitCode == 0 {
		return LoginAuthenticated
	}
	return LoginRequired
}
func authFileStatus(path string) AuthFileStatus {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return AuthMissing
	}
	if err != nil {
		return AuthMissing
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return AuthSymlink
	}
	if !info.Mode().IsRegular() {
		return AuthUnsafeMode
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return AuthWrongOwner
	}
	if info.Mode().Perm()&0o077 != 0 {
		return AuthUnsafeMode
	}
	return AuthRegular
}
func safeVersion(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	return !strings.ContainsAny(s, "\x00\r\n")
}
