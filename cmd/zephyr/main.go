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
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/signaturekey/zephyr/internal/codexevents"
	"github.com/signaturekey/zephyr/internal/harnessinstall"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/signaturekey/zephyr/internal/workflow"
)

var (
	version = "dev"
	commit  = "unknown"
	dirty   = "unknown"
)

type CLI struct {
	RunRoot string `name:"run-root" env:"ZEPHYR_RUN_ROOT" help:"Корень хранилища запусков (по умолчанию: XDG cache или ~/.cache/zephyr/runs)." type:"path"`

	Init               InitCmd               `cmd:"" help:"Создать неизменяемый запуск вне проверяемого репозитория."`
	Collect            CollectCmd            `cmd:"" help:"Собрать read-only снимок через системный Git."`
	Context            ContextCmd            `cmd:"" help:"Зафиксировать возможности harness, импортировать бизнес-контекст или добавить ограничения покрытия."`
	Route              RouteCmd              `cmd:"" help:"Собрать и проверить пакет, затем выбрать роли ревьюеров."`
	ValidateRouting    ValidateRoutingCmd    `cmd:"" name:"validate-routing" help:"Проверить semantic routing и зафиксировать итоговый набор ролей."`
	FallbackRouting    FallbackRoutingCmd    `cmd:"" name:"fallback-routing" help:"Завершить routing консервативным deterministic fallback."`
	ValidateCandidates ValidateCandidatesCmd `cmd:"" name:"validate-candidates" help:"Проверить JSON одного изолированного ревьюера и выполнить precheck."`
	ValidateVerdicts   ValidateVerdictsCmd   `cmd:"" name:"validate-verdicts" help:"Проверить JSON evidence-gate относительно точного набора кандидатов."`
	RecoverCodexOutput RecoverCodexOutputCmd `cmd:"" name:"recover-codex-output" hidden:""`
	MarkFailed         MarkFailedCmd         `cmd:"" name:"mark-failed" help:"Зафиксировать сбой ревьюера или evidence-gate, не теряя остальные результаты."`
	Aggregate          AggregateCmd          `cmd:"" help:"Применить вердикты, устранить дубли и создать review.json."`
	Render             RenderCmd             `cmd:"" help:"Сформировать review.md из проверенного review.json."`
	Inspect            InspectCmd            `cmd:"" help:"Показать состояние запуска, счётчики, ограничения и пути к артефактам."`
	Harness            HarnessCmd            `cmd:"" help:"Установить встроенные skills и agents Zephyr в локальный harness."`
	Version            VersionCmd            `cmd:"" help:"Вывести версию сборки Zephyr."`
}

type RecoverCodexOutputCmd struct {
	Kind   string `required:"" enum:"reviewer,routing,evidence" help:"Тип изолированного процесса."`
	Input  string `required:"" type:"path" help:"JSONL event stream Codex CLI."`
	Output string `required:"" type:"path" help:"Новый файл восстановленного structured output."`
}

func (command *RecoverCodexOutputCmd) Run(app *runtime) error {
	if !filepath.IsAbs(command.Output) {
		return errors.New("--output must be absolute")
	}
	if info, err := os.Lstat(command.Output); err == nil {
		return fmt.Errorf("output %q already exists with mode %s", command.Output, info.Mode())
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect output %q: %w", command.Output, err)
	}
	data, err := readInput(app.stdin, command.Input, 64<<20)
	if err != nil {
		return err
	}
	output, err := codexevents.Recover(data, codexevents.Kind(command.Kind))
	if err != nil {
		return err
	}
	return codexevents.WriteRecovered(app.ctx, command.Output, output)
}

type runtime struct {
	ctx     context.Context
	service *workflow.Service
	stdin   io.Reader
	stdout  io.Writer
}

type HarnessCmd struct {
	Install   HarnessInstallCmd   `cmd:"" help:"Установить встроенные ресурсы Zephyr."`
	Uninstall HarnessUninstallCmd `cmd:"" help:"Удалить встроенные ресурсы Zephyr."`
}

type HarnessInstallCmd struct{}

func (command *HarnessInstallCmd) Run(app *runtime) error {
	options, err := harnessinstall.OptionsFromEnvironment()
	if err != nil {
		return err
	}
	result, err := harnessinstall.Install(options)
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type HarnessUninstallCmd struct{}

func (command *HarnessUninstallCmd) Run(app *runtime) error {
	options, err := harnessinstall.OptionsFromEnvironment()
	if err != nil {
		return err
	}
	result, err := harnessinstall.Uninstall(options)
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
		kong.Description("Локальное read-only ядро ревью с проверкой доказательств для Codex."),
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
	Repo   string `default:"." help:"Каталог репозитория." type:"path"`
	Mode   string `default:"auto" enum:"auto,plan,implementation,alignment" help:"Режим ревью."`
	Source string `help:"Git-область: working-tree, staged, branch, commit-range или plan-only; без значения определяется автоматически."`
	Base   string `help:"Базовая ссылка для ревью ветки; задаёт source=branch."`
	Range  string `name:"range" help:"Диапазон коммитов A..B или A...B; задаёт source=commit-range."`
	Plan   string `help:"План или change-spec для снимка." type:"path"`
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
	RunID            string `name:"run" required:"" help:"ID запуска, возвращённый init."`
	IncludeGenerated bool   `help:"Включить содержимое сгенерированных файлов (restricted paths всё равно исключаются)."`
	IncludeVendor    bool   `help:"Включить содержимое vendor-файлов (restricted paths всё равно исключаются)."`
	IncludeUntracked bool   `name:"include-untracked" help:"Явно включить безопасное ограниченное содержимое неотслеживаемых файлов."`
	MaxUntracked     int64  `name:"max-untracked-bytes" help:"Максимальный размер каждого явно включённого неотслеживаемого файла."`
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
	Add        ContextAddCmd        `cmd:"" help:"Импортировать нормализованный снимок Jira, Confluence или Bitbucket до routing."`
	Capability ContextCapabilityCmd `cmd:"" help:"Зафиксировать необходимый перед routing статус возможности harness."`
	Limit      ContextLimitCmd      `cmd:"" help:"Зафиксировать недоступный или усечённый источник."`
}

type ContextCapabilityCmd struct {
	RunID  string `name:"run" required:"" help:"ID запуска."`
	Source string `required:"" enum:"jira,confluence,bitbucket" help:"Источник возможности harness."`
	Status string `required:"" enum:"available,unavailable,not-required" help:"Статус возможности для этого запуска."`
	Reason string `help:"Краткая причина; обязательна при unavailable и not-required."`
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
	RunID  string `name:"run" required:"" help:"ID запуска."`
	Source string `required:"" enum:"jira,confluence,bitbucket" help:"Бизнес-источник."`
	Key    string `required:"" help:"Стабильный ключ задачи или ID страницы/объекта."`
	URL    string `help:"URL источника, если доступен."`
	Input  string `required:"" help:"Нормализованный Markdown-файл или - для stdin."`
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
	RunID  string `name:"run" required:"" help:"ID запуска."`
	Source string `required:"" help:"Имя недоступного или усечённого источника."`
	Reason string `required:"" help:"Краткое ограничение покрытия."`
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
	RunID       string   `name:"run" required:"" help:"ID запуска."`
	AddRole     []string `name:"add-role" help:"Принудительно включить известную роль ревьюера; можно повторять."`
	ExcludeRole []string `name:"exclude-role" help:"Принудительно исключить необязательную роль ревьюера; можно повторять."`
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

type ValidateRoutingCmd struct {
	RunID string `name:"run" required:"" help:"ID запуска."`
	Input string `required:"" help:"JSON-файл semantic router или - для stdin."`
}

func (command *ValidateRoutingCmd) Run(app *runtime) error {
	data, err := readInput(app.stdin, command.Input, 8<<20)
	if err != nil {
		return err
	}
	result, err := app.service.ValidateRouting(app.ctx, workflow.ValidateRoutingOptions{RunID: command.RunID, Input: data})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type FallbackRoutingCmd struct {
	RunID  string `name:"run" required:"" help:"ID запуска."`
	Reason string `required:"" help:"Краткая безопасная причина fallback."`
}

func (command *FallbackRoutingCmd) Run(app *runtime) error {
	result, err := app.service.FallbackRouting(app.ctx, workflow.FinalizeRoutingOptions{RunID: command.RunID, Reason: command.Reason})
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type ValidateCandidatesCmd struct {
	RunID string `name:"run" required:"" help:"ID запуска."`
	Role  string `required:"" help:"Выбранная роль ревьюера."`
	Input string `required:"" help:"JSON-файл ревьюера или - для stdin."`
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
	RunID string `name:"run" required:"" help:"ID запуска."`
	Input string `required:"" help:"JSON-файл evidence-gate или - для stdin."`
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
	RunID  string `name:"run" required:"" help:"ID запуска."`
	Stage  string `required:"" enum:"review,evidence" help:"Сбойный этап harness."`
	Role   string `help:"Выбранная роль; обязательна только при stage=review."`
	Reason string `required:"" help:"Краткая безопасная причина сбоя."`
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
	RunID string `name:"run" required:"" help:"ID запуска."`
}

func (command *AggregateCmd) Run(app *runtime) error {
	result, err := app.service.Aggregate(app.ctx, command.RunID)
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type RenderCmd struct {
	RunID     string `name:"run" required:"" help:"ID запуска."`
	IncludeP3 bool   `name:"include-p3" help:"Включить P3-находки, даже когда есть более высокие приоритеты."`
}

func (command *RenderCmd) Run(app *runtime) error {
	result, err := app.service.Render(app.ctx, command.RunID, command.IncludeP3)
	if err != nil {
		return err
	}
	return emit(app.stdout, result)
}

type InspectCmd struct {
	RunID string `name:"run" required:"" help:"ID запуска."`
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
		Version         string `json:"version"`
		Commit          string `json:"commit"`
		Dirty           string `json:"dirty"`
		ProtocolVersion int    `json:"protocol_version"`
	}{
		Version:         version,
		Commit:          commit,
		Dirty:           dirty,
		ProtocolVersion: schema.ProtocolVersion,
	})
}
