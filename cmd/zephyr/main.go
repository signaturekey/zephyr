package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/signaturekey/zephyr/internal/harnessinstall"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/workflow"
)

var version = "dev"

type CLI struct {
	RunRoot string `name:"run-root" env:"ZEPHYR_RUN_ROOT" help:"Run store root (default: XDG cache or ~/.cache/zephyr/runs)." type:"path"`

	Init               InitCmd               `cmd:"" help:"Create an immutable run outside the reviewed repository."`
	Collect            CollectCmd            `cmd:"" help:"Collect a read-only system-Git snapshot."`
	Context            ContextCmd            `cmd:"" help:"Record harness capabilities, import frozen business context, or add coverage limits."`
	Route              RouteCmd              `cmd:"" help:"Build and validate the packet, then select reviewer roles."`
	ValidateCandidates ValidateCandidatesCmd `cmd:"" name:"validate-candidates" help:"Validate and precheck one isolated reviewer's JSON."`
	ValidateVerdicts   ValidateVerdictsCmd   `cmd:"" name:"validate-verdicts" help:"Validate the evidence-gate JSON against the exact candidate set."`
	MarkFailed         MarkFailedCmd         `cmd:"" name:"mark-failed" help:"Record a failed reviewer or evidence gate without losing other results."`
	Aggregate          AggregateCmd          `cmd:"" help:"Apply verdicts, deduplicate findings, and create review.json."`
	Render             RenderCmd             `cmd:"" help:"Render review.md from validated review.json."`
	Inspect            InspectCmd            `cmd:"" help:"Show run state, counts, limits, and artifact paths."`
	Harness            HarnessCmd            `cmd:"" help:"Install embedded Zephyr skills and agents into a local harness."`
	Version            VersionCmd            `cmd:"" help:"Print the Zephyr build version."`
}

type runtime struct {
	ctx     context.Context
	service *workflow.Service
	stdin   io.Reader
	stdout  io.Writer
}

type HarnessCmd struct {
	Install HarnessInstallCmd `cmd:"" help:"Install embedded Zephyr assets into Codex, Claude Code, or both."`
}

type HarnessInstallCmd struct {
	Surface string `arg:"" required:"" enum:"codex,claude,all" help:"Harness surface: codex, claude, or all."`
}

func (command *HarnessInstallCmd) Run(app *runtime) error {
	options, err := harnessinstall.OptionsFromEnvironment(harnessinstall.Surface(command.Surface))
	if err != nil {
		return err
	}
	result, err := harnessinstall.Install(options)
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("zephyr"),
		kong.Description("Local, read-only, evidence-gated review core for Codex App and Claude Code."),
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
		return 1
	}
	service, err := workflow.New(cli.RunRoot)
	if err != nil {
		fmt.Fprintf(stderr, "zephyr: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := parsed.Run(&runtime{ctx: ctx, service: service, stdin: stdin, stdout: stdout}); err != nil {
		fmt.Fprintf(stderr, "zephyr: %v\n", err)
		return 1
	}
	return 0
}

func emit(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("write command output: %w", err)
	}
	return nil
}

func readInput(reader io.Reader, path string, maximum int64) ([]byte, error) {
	if path == "" {
		return nil, errors.New("--input is required")
	}
	var (
		input io.Reader
		file  *os.File
		err   error
	)
	if path == "-" {
		input = reader
	} else {
		file, err = os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open input %q: %w", path, err)
		}
		defer file.Close()
		input = file
	}
	data, err := io.ReadAll(io.LimitReader(input, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read input %q: %w", path, err)
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("input %q exceeds %d bytes", path, maximum)
	}
	return data, nil
}

type InitCmd struct {
	Repo   string `default:"." help:"Repository directory." type:"path"`
	Mode   string `default:"auto" enum:"auto,plan,implementation,alignment" help:"Review mode."`
	Source string `help:"Git scope: working-tree, staged, branch, commit-range, or plan-only; inferred when omitted."`
	Base   string `help:"Base ref for branch review; implies source=branch."`
	Range  string `name:"range" help:"Commit range A..B or A...B; implies source=commit-range."`
	Plan   string `help:"Plan or change-spec to snapshot." type:"path"`
}

func (command *InitCmd) Run(app *runtime) error {
	result, err := app.service.Init(app.ctx, workflow.InitOptions{
		Repository: command.Repo, Mode: run.Mode(command.Mode), Source: run.Source(command.Source),
		BaseRef: command.Base, CommitRange: command.Range, PlanPath: command.Plan,
	})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type CollectCmd struct {
	RunID            string `name:"run" required:"" help:"Run ID returned by init."`
	IncludeGenerated bool   `help:"Include generated file contents (restricted paths remain excluded)."`
	IncludeVendor    bool   `help:"Include vendor file contents (restricted paths remain excluded)."`
	IncludeUntracked bool   `name:"include-untracked" help:"Explicitly include safe, bounded untracked contents."`
	MaxUntracked     int64  `name:"max-untracked-bytes" help:"Maximum bytes per explicitly included untracked file."`
}

func (command *CollectCmd) Run(app *runtime) error {
	result, err := app.service.Collect(app.ctx, workflow.CollectOptions{
		RunID: command.RunID, IncludeGenerated: command.IncludeGenerated, IncludeVendor: command.IncludeVendor,
		IncludeUntrackedContent: command.IncludeUntracked, MaxUntrackedBytes: command.MaxUntracked,
	})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type ContextCmd struct {
	Add        ContextAddCmd        `cmd:"" help:"Import a normalized Jira, Confluence, or Bitbucket snapshot before routing."`
	Capability ContextCapabilityCmd `cmd:"" help:"Record the harness capability status required before routing."`
	Limit      ContextLimitCmd      `cmd:"" help:"Record an unavailable or truncated source."`
}

type ContextCapabilityCmd struct {
	RunID  string `name:"run" required:"" help:"Run ID."`
	Source string `required:"" enum:"jira,confluence,bitbucket" help:"Harness capability source."`
	Status string `required:"" enum:"available,unavailable,not-required" help:"Capability status for this run."`
	Reason string `help:"Concise reason; required for unavailable and not-required."`
}

func (command *ContextCapabilityCmd) Run(app *runtime) error {
	result, err := app.service.SetCapability(app.ctx, workflow.CapabilitySetOptions{
		RunID: command.RunID, Source: workflow.CapabilitySource(command.Source),
		Status: workflow.CapabilityStatus(command.Status), Reason: command.Reason,
	})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type ContextAddCmd struct {
	RunID  string `name:"run" required:"" help:"Run ID."`
	Source string `required:"" enum:"jira,confluence,bitbucket" help:"Business source."`
	Key    string `required:"" help:"Stable issue key or page/object ID."`
	URL    string `help:"Source URL, when available."`
	Input  string `required:"" help:"Normalized Markdown file, or - for stdin."`
}

func (command *ContextAddCmd) Run(app *runtime) error {
	data, err := readInput(app.stdin, command.Input, 16<<20)
	if err != nil {
		return err
	}
	result, err := app.service.AddContext(app.ctx, workflow.ContextAddOptions{
		RunID: command.RunID, Source: command.Source, Key: command.Key, URL: command.URL, Content: data,
	})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type ContextLimitCmd struct {
	RunID  string `name:"run" required:"" help:"Run ID."`
	Source string `required:"" help:"Unavailable or truncated source name."`
	Reason string `required:"" help:"Concise coverage limitation."`
}

func (command *ContextLimitCmd) Run(app *runtime) error {
	result, err := app.service.AddCoverage(app.ctx, workflow.CoverageAddOptions{
		RunID: command.RunID, Source: command.Source, Reason: command.Reason,
	})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type RouteCmd struct {
	RunID       string   `name:"run" required:"" help:"Run ID."`
	AddRole     []string `name:"add-role" help:"Force-include a known reviewer role; repeatable."`
	ExcludeRole []string `name:"exclude-role" help:"Force-exclude an optional reviewer role; repeatable."`
}

func (command *RouteCmd) Run(app *runtime) error {
	result, err := app.service.Route(app.ctx, workflow.RouteOptions{
		RunID: command.RunID, ForceInclude: command.AddRole, ForceExclude: command.ExcludeRole,
	})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type ValidateCandidatesCmd struct {
	RunID string `name:"run" required:"" help:"Run ID."`
	Role  string `required:"" help:"Selected reviewer role."`
	Input string `required:"" help:"Reviewer JSON file, or - for stdin."`
}

func (command *ValidateCandidatesCmd) Run(app *runtime) error {
	data, err := readInput(app.stdin, command.Input, 8<<20)
	if err != nil {
		return err
	}
	result, err := app.service.ValidateCandidates(app.ctx, workflow.ValidateCandidatesOptions{
		RunID: command.RunID, Role: command.Role, Input: data,
	})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type ValidateVerdictsCmd struct {
	RunID string `name:"run" required:"" help:"Run ID."`
	Input string `required:"" help:"Evidence-gate JSON file, or - for stdin."`
}

func (command *ValidateVerdictsCmd) Run(app *runtime) error {
	data, err := readInput(app.stdin, command.Input, 8<<20)
	if err != nil {
		return err
	}
	result, err := app.service.ValidateVerdicts(app.ctx, workflow.ValidateVerdictsOptions{RunID: command.RunID, Input: data})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type MarkFailedCmd struct {
	RunID  string `name:"run" required:"" help:"Run ID."`
	Stage  string `required:"" enum:"review,evidence" help:"Failed harness stage."`
	Role   string `help:"Selected role; required only for stage=review."`
	Reason string `required:"" help:"Concise safe failure reason."`
}

func (command *MarkFailedCmd) Run(app *runtime) error {
	result, err := app.service.MarkFailed(app.ctx, workflow.MarkFailedOptions{
		RunID: command.RunID, Stage: command.Stage, Role: command.Role, Reason: command.Reason,
	})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type AggregateCmd struct {
	RunID string `name:"run" required:"" help:"Run ID."`
}

func (command *AggregateCmd) Run(app *runtime) error {
	result, err := app.service.Aggregate(app.ctx, command.RunID)
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type RenderCmd struct {
	RunID     string `name:"run" required:"" help:"Run ID."`
	IncludeP3 bool   `name:"include-p3" help:"Include P3 findings even when higher priorities exist."`
}

func (command *RenderCmd) Run(app *runtime) error {
	result, err := app.service.Render(app.ctx, command.RunID, command.IncludeP3)
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type InspectCmd struct {
	RunID string `name:"run" required:"" help:"Run ID."`
}

func (command *InspectCmd) Run(app *runtime) error {
	result, err := app.service.Inspect(app.ctx, command.RunID)
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type VersionCmd struct{}

func (*VersionCmd) Run(app *runtime) error {
	return emit(app.stdout, struct {
		Version string `json:"version"`
	}{Version: version})
}
