package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/signaturekey/zephyr/internal/agent"
	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/review"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/snapshot"
)

var (
	version = "dev"
	commit  = "unknown"
	dirty   = "unknown"
)

type CLI struct {
	Review  ReviewCmd  `cmd:"" help:"Проверить один frozen snapshot через изолированные Codex-роли."`
	Version VersionCmd `cmd:"" help:"Вывести версию Zephyr."`
}

type ReviewCmd struct {
	Repo        string   `default:"." help:"Локальный Git repository или URL для commit/branch review."`
	Worktree    bool     `help:"Проверить текущий локальный worktree (default)."`
	Commit      string   `help:"Проверить один commit."`
	Branch      string   `help:"Проверить ветку относительно --base."`
	Base        string   `help:"Base ref для --branch."`
	Config      string   `help:"Project config override; default .zephyr/config.yaml из snapshot, затем встроенные defaults." type:"path"`
	Context     []string `help:"Frozen Markdown/JSON context; flag можно повторять." type:"path"`
	IncludeRole []string `name:"include-role" help:"Явно включить reviewer role; flag можно повторять."`
	ExcludeRole []string `name:"exclude-role" help:"Явно исключить optional reviewer role; flag можно повторять."`
	MaxParallel int      `name:"max-parallel" help:"Максимальное число одновременных reviewers; default из config."`
	Output      string   `help:"Сохранить Markdown report; по умолчанию Markdown пишется в stdout." type:"path"`
	JSONOutput  string   `name:"json-output" help:"Сохранить canonical JSON report." type:"path"`
	KeepTemp    bool     `name:"keep-temp" help:"Не удалять disposable snapshot после review (только для диагностики)."`
}

type VersionCmd struct{}

type runtime struct {
	ctx     context.Context
	stdout  io.Writer
	stderr  io.Writer
	service review.Service
}

func main() { os.Exit(runMain(os.Args[1:], os.Stdout, os.Stderr)) }

func runMain(args []string, stdout, stderr io.Writer) int {
	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("zephyr"),
		kong.Description("Локальный evidence-gated code review поверх Aether и Codex App Server."),
		kong.UsageOnError(),
		kong.Writers(stdout, stderr),
	)
	if err != nil {
		fmt.Fprintf(stderr, "zephyr: initialize CLI: %v\n", err)
		return 1
	}
	parsed, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "zephyr: %v\n", err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service := review.Service{
		RuntimeFactory: func(ctx context.Context, cfg config.Config) (agent.Runtime, error) {
			return agent.Start(ctx, cfg, version)
		},
	}
	if err := parsed.Run(&runtime{ctx: ctx, stdout: stdout, stderr: stderr, service: service}); err != nil {
		fmt.Fprintf(stderr, "zephyr: %v\n", err)
		if errors.Is(err, review.ErrInvalidRequest) || errors.Is(err, routing.ErrInvalid) {
			return 2
		}
		return 1
	}
	return 0
}

func (command *ReviewCmd) Run(app *runtime) error {
	source, err := command.source()
	if err != nil {
		return err
	}
	result, err := app.service.Run(app.ctx, review.Request{
		Repository: command.Repo, Source: source, Commit: command.Commit, Branch: command.Branch, Base: command.Base,
		ConfigPath: command.Config, Contexts: command.Context, IncludeRole: command.IncludeRole,
		ExcludeRole: command.ExcludeRole, MaxParallel: command.MaxParallel, KeepTemp: command.KeepTemp,
	})
	if err != nil {
		return err
	}
	if command.Output != "" {
		if err := writeOutput(command.Output, result.Markdown); err != nil {
			return err
		}
	}
	if command.JSONOutput != "" {
		if err := writeOutput(command.JSONOutput, result.JSON); err != nil {
			return err
		}
	}
	if _, err := app.stdout.Write(result.Markdown); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	if command.KeepTemp {
		fmt.Fprintf(app.stderr, "zephyr: kept snapshot at %s\n", result.SnapshotRoot)
	}
	return nil
}

func (command ReviewCmd) source() (snapshot.Source, error) {
	selectors := 0
	if command.Worktree {
		selectors++
	}
	if command.Commit != "" {
		selectors++
	}
	if command.Branch != "" {
		selectors++
	}
	if selectors > 1 {
		return "", fmt.Errorf("%w: choose exactly one of --worktree, --commit or --branch", review.ErrInvalidRequest)
	}
	if command.Base != "" && command.Branch == "" {
		return "", fmt.Errorf("%w: --base requires --branch", review.ErrInvalidRequest)
	}
	if command.Branch != "" {
		if command.Base == "" {
			return "", fmt.Errorf("%w: --branch requires --base", review.ErrInvalidRequest)
		}
		return snapshot.SourceBranch, nil
	}
	if command.Commit != "" {
		return snapshot.SourceCommit, nil
	}
	return snapshot.SourceWorktree, nil
}

func (command *VersionCmd) Run(app *runtime) error {
	_, err := fmt.Fprintf(app.stdout, "zephyr %s (commit=%s dirty=%s)\n", version, commit, dirty)
	return err
}

func writeOutput(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write output %q: %w", path, err)
	}
	return nil
}
