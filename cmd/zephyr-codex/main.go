package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/signaturekey/zephyr/internal/codexharness/compatibility"
	"github.com/signaturekey/zephyr/internal/codexharness/diagnostics"
	"github.com/signaturekey/zephyr/internal/codexharness/dispatch"
	"github.com/signaturekey/zephyr/internal/codexharness/environment"
	"github.com/signaturekey/zephyr/internal/codexharness/layout"
	"github.com/signaturekey/zephyr/internal/codexharness/preflight"
	"github.com/signaturekey/zephyr/internal/codexharness/process"
	"github.com/signaturekey/zephyr/internal/codexharness/review"
	"github.com/signaturekey/zephyr/internal/protocol"
)

var (
	version = "dev"
	commit  = "unknown"
	dirty   = "unknown"
)

type cli struct {
	Doctor  doctorCommand  `cmd:"" help:"Проверить локальные предпосылки экспериментального Zephyr Codex driver."`
	Review  reviewCommand  `cmd:"" help:"Запустить experimental local working-tree implementation review."`
	Version versionCommand `cmd:"" help:"Вывести версию experimental Zephyr Codex driver."`
}

type doctorCommand struct {
	KeepPrivateDiagnostics bool `name:"keep-private-diagnostics" help:"Не удалять приватные диагностические файлы."`
}

type reviewCommand struct {
	Repository             string `name:"repo" required:"" type:"path" help:"Абсолютный путь к локальному Git-репозиторию."`
	KeepPrivateDiagnostics bool   `name:"keep-private-diagnostics" help:"Не удалять приватные диагностические файлы."`
}

type versionCommand struct{}

type versionResult struct {
	Version                string `json:"version"`
	Commit                 string `json:"commit"`
	Dirty                  string `json:"dirty"`
	CodexHarnessAPIVersion int    `json:"codex_harness_api_version"`
}

type application interface {
	Doctor(context.Context, bool) review.DoctorResult
	Review(context.Context, review.ReviewOptions) (review.Result, error)
}

type applicationFactory func() (application, error)

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, newApplication))
}

func runMain(args []string, _ io.Reader, stdout, stderr io.Writer, factory applicationFactory) int {
	if err := validateRepositoryArgument(args); err != nil {
		fmt.Fprintln(stderr, "zephyr-codex: --repo must be absolute")
		return 1
	}
	var command cli
	parser, err := kong.New(&command, kong.Name("zephyr-codex"), kong.UsageOnError(), kong.Writers(stdout, stderr))
	if err != nil {
		fmt.Fprintln(stderr, "zephyr-codex: initialize CLI")
		return 1
	}
	parsed, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintln(stderr, "zephyr-codex: invalid command input")
		return 1
	}
	if command.Review.Repository != "" && !filepath.IsAbs(command.Review.Repository) {
		fmt.Fprintln(stderr, "zephyr-codex: --repo must be absolute")
		return 1
	}
	if command.Doctor.KeepPrivateDiagnostics || command.Review.KeepPrivateDiagnostics {
		fmt.Fprintln(stderr, "zephyr-codex: warning: retained files may contain proprietary code/model output")
	}
	switch parsed.Command() {
	case "doctor":
		app, err := factory()
		if err != nil {
			fmt.Fprintln(stderr, "zephyr-codex: doctor setup failed")
			return 1
		}
		fmt.Fprintln(stderr, "zephyr-codex: running doctor")
		result := app.Doctor(commandContext(), command.Doctor.KeepPrivateDiagnostics)
		if err := emit(stdout, result); err != nil {
			fmt.Fprintln(stderr, "zephyr-codex: write output failed")
			return 1
		}
		if result.OK {
			return 0
		}
		return 1
	case "review":
		app, err := factory()
		if err != nil {
			fmt.Fprintln(stderr, "zephyr-codex: review setup failed")
			return 1
		}
		fmt.Fprintln(stderr, "zephyr-codex: starting review")
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		result, reviewErr := app.Review(ctx, review.ReviewOptions{Repository: command.Review.Repository, KeepPrivateDiagnostics: command.Review.KeepPrivateDiagnostics})
		if result.DiagnosticsPath != "" {
			if err := emit(stdout, result); err != nil {
				fmt.Fprintln(stderr, "zephyr-codex: write output failed")
				return 1
			}
		}
		if reviewErr != nil || (result.Status != string(review.StatusComplete) && result.Status != string(review.StatusCompleteWithLimits)) {
			fmt.Fprintln(stderr, "zephyr-codex: review did not complete")
			return 1
		}
		return 0
	case "version":
		if err := emit(stdout, versionResult{Version: version, Commit: commit, Dirty: dirty, CodexHarnessAPIVersion: protocol.CodexHarnessAPIVersion}); err != nil {
			fmt.Fprintln(stderr, "zephyr-codex: write output failed")
			return 1
		}
		return 0
	default:
		fmt.Fprintln(stderr, "zephyr-codex: unknown command")
		return 1
	}
}

func validateRepositoryArgument(args []string) error {
	for index, argument := range args {
		value := ""
		switch {
		case argument == "--repo":
			if index+1 >= len(args) {
				return errors.New("missing repository")
			}
			value = args[index+1]
		case strings.HasPrefix(argument, "--repo="):
			value = strings.TrimPrefix(argument, "--repo=")
		default:
			continue
		}
		if !filepath.IsAbs(value) {
			return errors.New("repository is relative")
		}
	}
	return nil
}

func commandContext() context.Context {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() { <-ctx.Done(); stop() }()
	return ctx
}

func emit(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

type productionApplication struct {
	doctorFactory func(bool) *review.Doctor
	review        *review.Service
}

func (app *productionApplication) Doctor(ctx context.Context, keep bool) review.DoctorResult {
	return app.doctorFactory(keep).Run(ctx)
}

func (app *productionApplication) Review(ctx context.Context, options review.ReviewOptions) (review.Result, error) {
	return app.review.Review(ctx, options)
}

func newApplication() (application, error) {
	driverRoot, err := configuredDriverRoot()
	if err != nil {
		return nil, err
	}
	runner := process.ExecRunner{}
	checker := preflight.New(preflight.Options{
		ZephyrPath:     os.Getenv("ZEPHYR_CORE_BIN"),
		CodexPath:      os.Getenv("ZEPHYR_CODEX_BIN"),
		DispatcherPath: os.Getenv("ZEPHYR_CODEX_DISPATCHER"),
		CodexHome:      os.Getenv("CODEX_HOME"),
		Runner:         runner,
		CoreEnv:        preflightEnvironment(driverRoot),
	})
	service := review.NewService(review.Dependencies{
		DriverRoot: driverRoot,
		Preflight:  checker,
		CoreFactory: func(result preflight.Result, runRoot string) review.Core {
			client := review.NewCoreClient(result.ZephyrPath, runRoot, runner)
			client.Env = coreEnvironment(runRoot)
			return client
		},
		DispatcherFactory: func(result preflight.Result) dispatch.Client {
			return dispatcherClient(result, runner, "")
		},
		CompatibilityFactory: func(result preflight.Result, roots layout.Roots, client dispatch.Client) (review.Compatibility, error) {
			return newCompatibility(result, roots, client)
		},
		Finish: review.FinalizeDiagnostics,
	})
	doctorFactory := func(keep bool) *review.Doctor {
		remembering := &rememberingPreflight{checker: checker}
		return review.NewDoctor(review.DoctorDependencies{
			Preflight:      remembering,
			Compatibility:  doctorCompatibility{preflight: remembering, driverRoot: driverRoot},
			BeginOperation: doctorOperationFactory(driverRoot, keep),
			WritePolicy:    compatibility.WriteDoctorPolicy,
		})
	}
	return &productionApplication{doctorFactory: doctorFactory, review: service}, nil
}

func newCompatibility(result preflight.Result, roots layout.Roots, client dispatch.Client) (review.Compatibility, error) {
	cache, err := compatibility.NewCache(roots.CacheRoot)
	if err != nil {
		return nil, err
	}
	return &compatibility.Manager{Cache: cache, Checker: client, CodexPath: result.CodexPath, DispatcherPath: result.DispatcherPath}, nil
}

type rememberingPreflight struct {
	checker review.HostPreflight
	mu      sync.Mutex
	result  preflight.Result
	ok      bool
}

func (remembering *rememberingPreflight) Check(ctx context.Context) (preflight.Result, error) {
	result, err := remembering.checker.Check(ctx)
	if err == nil {
		remembering.mu.Lock()
		remembering.result, remembering.ok = result, true
		remembering.mu.Unlock()
	}
	return result, err
}

func (remembering *rememberingPreflight) Result() (preflight.Result, error) {
	remembering.mu.Lock()
	defer remembering.mu.Unlock()
	if !remembering.ok {
		return preflight.Result{}, errors.New("preflight result is unavailable")
	}
	return remembering.result, nil
}

type doctorCompatibility struct {
	preflight  *rememberingPreflight
	driverRoot string
}

func (adapter doctorCompatibility) Ensure(ctx context.Context, policy, operationDir, privateDir string) (compatibility.Result, error) {
	result, err := adapter.preflight.Result()
	if err != nil {
		return compatibility.Result{}, err
	}
	roots, err := doctorRoots(adapter.driverRoot)
	if err != nil {
		return compatibility.Result{}, err
	}
	client := dispatcherClient(result, process.ExecRunner{}, roots.RunRoot)
	manager, err := newCompatibility(result, roots, client)
	if err != nil {
		return compatibility.Result{}, err
	}
	return manager.Ensure(ctx, policy, operationDir, privateDir)
}

func coreEnvironment(runRoot string) []string {
	return environment.Core(environment.Inputs{PATH: os.Getenv("PATH"), TempDir: os.TempDir(), RunRoot: runRoot})
}

func preflightEnvironment(driverRoot string) []string {
	return environment.Core(environment.Inputs{
		PATH:    os.Getenv("PATH"),
		TempDir: os.TempDir(),
		RunRoot: filepath.Join(driverRoot, "runs"),
	})
}

func dispatcherClient(result preflight.Result, runner process.Runner, runRoot string) *dispatch.ScriptClient {
	client := dispatch.New(result.DispatcherPath, runner)
	client.Env = environment.Dispatcher(environment.Inputs{
		PATH:            os.Getenv("PATH"),
		Home:            os.TempDir(),
		TempDir:         os.TempDir(),
		RunRoot:         runRoot,
		CodexPath:       result.CodexPath,
		CorePath:        result.ZephyrPath,
		ProbeTimeout:    130 * time.Second,
		DispatchTimeout: 31 * time.Minute,
	})
	return client
}

func doctorOperationFactory(driverRoot string, keepPrivate bool) func() (diagnostics.Operation, error) {
	return func() (diagnostics.Operation, error) {
		roots, err := doctorRoots(driverRoot)
		if err != nil {
			return diagnostics.Operation{}, err
		}
		options := []diagnostics.StoreOption{}
		if keepPrivate {
			options = append(options, diagnostics.WithPrivateDiagnostics())
		}
		store, err := diagnostics.NewStore(roots, options...)
		if err != nil {
			return diagnostics.Operation{}, err
		}
		return store.Begin()
	}
}

func doctorRoots(driverRoot string) (layout.Roots, error) {
	if !filepath.IsAbs(driverRoot) {
		return layout.Roots{}, errors.New("driver root must be absolute")
	}
	roots := layout.Roots{DriverRoot: filepath.Clean(driverRoot)}
	roots.Operation = filepath.Join(roots.DriverRoot, "operations")
	roots.RunRoot = filepath.Join(roots.DriverRoot, "runs")
	roots.CacheRoot = filepath.Join(roots.DriverRoot, "cache")
	if err := layout.ValidateManagedRoots(roots); err != nil {
		return layout.Roots{}, err
	}
	return roots, nil
}

func configuredDriverRoot() (string, error) {
	if root := strings.TrimSpace(os.Getenv("ZEPHYR_CODEX_DRIVER_ROOT")); root != "" {
		if !filepath.IsAbs(root) {
			return "", errors.New("ZEPHYR_CODEX_DRIVER_ROOT must be absolute")
		}
		return filepath.Clean(root), nil
	}
	if cache := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); cache != "" {
		if !filepath.IsAbs(cache) {
			return "", errors.New("XDG_CACHE_HOME must be absolute")
		}
		return filepath.Join(cache, "zephyr", "codex"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "zephyr", "codex"), nil
}
