package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	"github.com/signaturekey/zephyr/internal/codexharness/budget"
	"github.com/signaturekey/zephyr/internal/codexharness/compatibility"
	"github.com/signaturekey/zephyr/internal/codexharness/diagnostics"
	"github.com/signaturekey/zephyr/internal/codexharness/dispatch"
	"github.com/signaturekey/zephyr/internal/codexharness/layout"
	"github.com/signaturekey/zephyr/internal/codexharness/preflight"
	"github.com/signaturekey/zephyr/internal/codexharness/scheduler"
)

var ErrIncompleteReview = errors.New("review incomplete")

type ReviewOptions struct {
	Repository             string
	KeepPrivateDiagnostics bool
}

type Result struct {
	OperationID       string   `json:"operation_id"`
	RunID             string   `json:"run_id,omitempty"`
	Status            string   `json:"status"`
	FailedStage       string   `json:"failed_stage,omitempty"`
	FailedRoles       []string `json:"failed_roles"`
	DiagnosticsPath   string   `json:"diagnostics_path"`
	ReviewJSON        string   `json:"review_json,omitempty"`
	ReviewMarkdown    string   `json:"review_markdown,omitempty"`
	ConfirmedFindings int      `json:"confirmed_findings"`
}

type Operation struct {
	ID, Root, DiagnosticsPath, OutputsDir, PrivateDir string
}

type Prepared struct {
	Repository string
	Roots      layout.Roots
	Operation  Operation
}

type PrepareFunc func(repository string, keepPrivate bool) (Prepared, error)
type HostPreflight interface {
	Check(context.Context) (preflight.Result, error)
}
type Compatibility interface {
	Ensure(context.Context, string, string, string) (compatibility.Result, error)
}
type ReviewerScheduler interface {
	Run(context.Context, []scheduler.Job, time.Duration) []scheduler.Result
}

type Dependencies struct {
	DriverRoot           string
	Prepare              PrepareFunc
	Preflight            HostPreflight
	Core                 Core
	CoreFactory          func(preflight.Result, string) Core
	Dispatcher           dispatch.Client
	DispatcherFactory    func(preflight.Result) dispatch.Client
	Compatibility        Compatibility
	CompatibilityFactory func(preflight.Result, layout.Roots, dispatch.Client) (Compatibility, error)
	Scheduler            ReviewerScheduler
	TotalBudget          time.Duration
	Finish               func(context.Context, Prepared, Result) error
	Lifecycle            Diagnostics
}

type Service struct{ dependencies Dependencies }

func NewService(dependencies Dependencies) *Service       { return &Service{dependencies: dependencies} }
func NewReviewService(dependencies Dependencies) *Service { return NewService(dependencies) }

func (service *Service) Review(parent context.Context, options ReviewOptions) (result Result, returnErr error) {
	result.FailedRoles = []string{}
	state := NewState("", service.dependencies.Lifecycle)
	if parent == nil {
		parent = context.Background()
	}
	limit := service.dependencies.TotalBudget
	if limit <= 0 {
		limit = budget.ReviewTotal
	}
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()

	prepare := service.dependencies.Prepare
	if prepare == nil {
		prepare = defaultPrepare(service.dependencies.DriverRoot)
	}
	prepared, err := prepare(options.Repository, options.KeepPrivateDiagnostics)
	if err != nil {
		return failResult(state, result, StageHostPreflight, err)
	}
	result.OperationID, result.DiagnosticsPath = prepared.Operation.ID, prepared.Operation.DiagnosticsPath
	defer func() {
		if service.dependencies.Finish != nil {
			if err := service.dependencies.Finish(context.WithoutCancel(parent), prepared, result); returnErr == nil && err != nil {
				returnErr = err
			}
		}
	}()

	if service.dependencies.Preflight == nil {
		return failResult(state, result, StageHostPreflight, errors.New("host preflight is not configured"))
	}
	preflightResult, err := service.dependencies.Preflight.Check(ctx)
	if err != nil {
		return failResult(state, result, StageHostPreflight, err)
	}
	if err := state.Complete(StageHostPreflight); err != nil {
		return result, err
	}

	core := service.dependencies.Core
	if core == nil && service.dependencies.CoreFactory != nil {
		core = service.dependencies.CoreFactory(preflightResult, prepared.Roots.RunRoot)
	}
	if core == nil {
		return failResult(state, result, StageCoreInit, errors.New("core is not configured"))
	}
	initialized, err := core.Init(ctx, prepared.Repository)
	if err != nil {
		return failResult(state, result, StageCoreInit, err)
	}
	result.RunID = initialized.RunID
	state.SetRunID(result.RunID)
	if err := state.Complete(StageCoreInit); err != nil {
		return result, err
	}
	defer func() {
		inspection, inspectErr := core.Inspect(context.WithoutCancel(parent), result.RunID)
		if inspectErr == nil {
			result.ReviewJSON = inspection.Artifacts.ReviewJSON
			result.ReviewMarkdown = inspection.Artifacts.ReviewMarkdown
			result.ConfirmedFindings = inspection.Counts.ConfirmedFindings
		}
		if returnErr == nil && inspectErr != nil {
			returnErr = inspectErr
		}
		if inspectErr == nil {
			if err := state.FinalizeInspect(); returnErr == nil && err != nil {
				returnErr = err
			}
		}
	}()

	collected, err := core.Collect(ctx, result.RunID)
	if err != nil {
		return failResult(state, result, StageCollection, err)
	}
	if !collected.Reviewable {
		return incompleteResult(state, result, StageCollection, ErrIncompleteReview)
	}
	if err := state.Complete(StageCollection); err != nil {
		return result, err
	}
	for _, source := range []string{"jira", "confluence", "bitbucket"} {
		if err := core.SetCapability(ctx, result.RunID, source); err != nil {
			return failResult(state, result, StageCapabilities, err)
		}
	}
	if err := state.Complete(StageCapabilities); err != nil {
		return result, err
	}

	dispatcher := service.dependencies.Dispatcher
	if dispatcher == nil && service.dependencies.DispatcherFactory != nil {
		dispatcher = service.dependencies.DispatcherFactory(preflightResult)
	}
	compat := service.dependencies.Compatibility
	if compat == nil && service.dependencies.CompatibilityFactory != nil {
		compat, err = service.dependencies.CompatibilityFactory(preflightResult, prepared.Roots, dispatcher)
		if err != nil {
			return failResult(state, result, StageCompatibility, err)
		}
	}
	if compat == nil {
		return failResult(state, result, StageCompatibility, errors.New("compatibility manager is not configured"))
	}
	compatible, err := compat.Ensure(ctx, collected.ModelPolicyPath, prepared.Operation.OutputsDir, prepared.Operation.PrivateDir)
	if err != nil {
		return failResult(state, result, StageCompatibility, err)
	}
	if err := state.Complete(StageCompatibility); err != nil {
		return result, err
	}

	routed, err := core.Route(ctx, result.RunID)
	if err != nil {
		return failResult(state, result, StageRoute, err)
	}
	finalRouting := routed.Routing
	semanticCount, err := semanticCandidateCount(routed.RoutingRequest)
	if err != nil {
		return failResult(state, result, StageRoute, err)
	}
	if err := state.Complete(StageRoute); err != nil {
		return result, err
	}
	degraded := false
	if semanticCount > 0 {
		if dispatcher == nil {
			return failResult(state, result, StageSemantic, errors.New("dispatcher is not configured"))
		}
		output := filepath.Join(prepared.Operation.OutputsDir, "semantic-routing.json")
		dispatched, dispatchErr := dispatcher.Route(ctx, dispatch.RoutingRequest{Common: dispatch.Common{PolicyPath: collected.ModelPolicyPath, CompatibilityPath: compatible.DescriptorPath, OutputPath: output, PrivateDiagnosticsDir: prepared.Operation.PrivateDir}, PacketPath: routed.PacketPath, RequestPath: routed.RoutingRequestPath})
		if dispatchErr != nil {
			fallback, fallbackErr := core.FallbackRouting(ctx, result.RunID, semanticFallbackReason(dispatched.Category))
			if fallbackErr != nil {
				return failResult(state, result, StageSemantic, fallbackErr)
			}
			finalRouting, degraded = fallback.Routing, true
		} else {
			finalized, validationErr := core.ValidateRouting(ctx, result.RunID, dispatched.OutputPath)
			if validationErr == nil {
				finalRouting = finalized.Routing
			} else if isCoreValidation(validationErr) {
				fallback, fallbackErr := core.FallbackRouting(ctx, result.RunID, "semantic-routing-validation-failed")
				if fallbackErr != nil {
					return failResult(state, result, StageSemantic, fallbackErr)
				}
				finalRouting, degraded = fallback.Routing, true
			} else {
				return failResult(state, result, StageSemantic, validationErr)
			}
		}
		if err := state.Complete(StageSemantic); err != nil {
			return result, err
		}
	} else if err := state.NotApplicable(StageSemantic); err != nil {
		return result, err
	}
	if degraded {
		if err := state.MarkDegraded(); err != nil {
			return result, err
		}
	}

	roles, err := selectedRoles(finalRouting)
	if err != nil {
		return failResult(state, result, StageReview, err)
	}
	scheduled := service.dependencies.Scheduler
	if scheduled == nil {
		scheduled = scheduler.Scheduler{InitialLimit: 4, DegradedLimit: 2}
	}
	var validated atomic.Int64
	jobs := make([]scheduler.Job, 0, len(roles))
	for _, selectedRole := range roles {
		role := selectedRole
		jobs = append(jobs, scheduler.Job{Role: role, Run: func(jobContext context.Context) scheduler.Result {
			return service.runReviewer(jobContext, core, dispatcher, prepared.Operation, collected.ModelPolicyPath, compatible.DescriptorPath, routed.PacketPath, result.RunID, role, &validated)
		}})
	}
	reviewerResults := scheduled.Run(ctx, jobs, budget.CleanupReserve)
	for _, reviewerResult := range reviewerResults {
		if reviewerResult.Err != nil {
			result.FailedRoles = append(result.FailedRoles, reviewerResult.Role)
		}
	}
	sort.Strings(result.FailedRoles)
	if validated.Load() == 0 {
		return incompleteResult(state, result, StageReview, ErrIncompleteReview)
	}
	if len(result.FailedRoles) > 0 {
		if err := state.MarkDegraded(); err != nil {
			return result, err
		}
	}
	if err := state.Complete(StageReview); err != nil {
		return result, err
	}
	evidenceInput, err := core.PrepareEvidence(ctx, result.RunID)
	if err != nil {
		_ = core.MarkEvidenceFailed(ctx, result.RunID, "evidence-input-failed")
		return incompleteResult(state, result, StageEvidenceInput, ErrIncompleteReview)
	}
	if err := state.Complete(StageEvidenceInput); err != nil {
		return result, err
	}
	if dispatcher == nil {
		return incompleteEvidenceResult(ctx, state, core, result, "evidence-dispatch-failed")
	}
	evidenceOutput := filepath.Join(prepared.Operation.OutputsDir, "evidence-verdicts.json")
	dispatchedEvidence, evidenceErr := dispatcher.Evidence(ctx, dispatch.EvidenceRequest{
		Common: dispatch.Common{
			PolicyPath:            collected.ModelPolicyPath,
			CompatibilityPath:     compatible.DescriptorPath,
			OutputPath:            evidenceOutput,
			PrivateDiagnosticsDir: prepared.Operation.PrivateDir,
		},
		PrecheckedPath: evidenceInput.CandidateSet,
		EvidencePath:   evidenceInput.Evidence,
	})
	if evidenceErr != nil {
		return incompleteEvidenceResult(ctx, state, core, result, evidenceFailureReason(dispatchedEvidence.Category))
	}
	verdictPath := dispatchedEvidence.OutputPath
	if verdictPath == "" {
		verdictPath = evidenceOutput
	}
	if _, err := core.ValidateVerdicts(ctx, result.RunID, verdictPath); err != nil {
		return incompleteEvidenceResult(ctx, state, core, result, "evidence-output-invalid")
	}
	if err := state.Complete(StageEvidence); err != nil {
		return result, err
	}
	if _, err := core.Aggregate(ctx, result.RunID); err != nil {
		return failResult(state, result, StageAggregate, err)
	}
	if err := state.Complete(StageAggregate); err != nil {
		return result, err
	}
	if _, err := core.Render(ctx, result.RunID); err != nil {
		return failResult(state, result, StageRender, err)
	}
	if err := state.Complete(StageRender); err != nil {
		return result, err
	}
	syncResultState(&result, state)
	return result, nil
}

func (service *Service) runReviewer(ctx context.Context, core Core, dispatcher dispatch.Client, operation Operation, policy, compatibilityPath, packet, runID, role string, validated *atomic.Int64) scheduler.Result {
	result := scheduler.Result{Role: role}
	if dispatcher == nil {
		result.Category, result.Err = diagnostics.CategoryLifecycle, errors.New("dispatcher is not configured")
		_ = core.MarkReviewerFailed(ctx, runID, role, "reviewer-dispatch-failed")
		return result
	}
	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			result.Category, result.Err = diagnostics.CategoryLifecycle, ctx.Err()
			return result
		}
		output := filepath.Join(operation.OutputsDir, fmt.Sprintf("review-%s-%d.json", role, attempt+1))
		dispatched, err := dispatcher.Review(ctx, dispatch.ReviewerRequest{Common: dispatch.Common{PolicyPath: policy, CompatibilityPath: compatibilityPath, OutputPath: output, PrivateDiagnosticsDir: operation.PrivateDir}, Role: role, PacketPath: packet, FormatRetry: attempt == 1})
		if err != nil {
			result.Category, result.Err = dispatched.Category, err
			_ = core.MarkReviewerFailed(ctx, runID, role, reviewerFailureReason(dispatched.Category))
			return result
		}
		input := dispatched.OutputPath
		if input == "" {
			input = output
		}
		_, err = core.ValidateCandidates(ctx, runID, role, input)
		if err == nil {
			validated.Add(1)
			return result
		}
		if isCoreValidation(err) && attempt == 0 {
			continue
		}
		result.Category, result.Err = diagnostics.CategoryValidation, err
		_ = core.MarkReviewerFailed(ctx, runID, role, "reviewer-output-invalid")
		return result
	}
	return result
}

func defaultPrepare(driverRoot string) PrepareFunc {
	return func(repository string, keep bool) (Prepared, error) {
		if !filepath.IsAbs(repository) {
			return Prepared{}, errors.New("repository must be absolute")
		}
		canonical, err := filepath.EvalSymlinks(filepath.Clean(repository))
		if err != nil {
			return Prepared{}, err
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			if err == nil {
				err = errors.New("repository is not a directory")
			}
			return Prepared{}, err
		}
		roots, err := layout.Resolve(canonical, driverRoot)
		if err != nil {
			return Prepared{}, err
		}
		options := []diagnostics.StoreOption{}
		if keep {
			options = append(options, diagnostics.WithPrivateDiagnostics())
		}
		store, err := diagnostics.NewStore(roots, options...)
		if err != nil {
			return Prepared{}, err
		}
		op, err := store.Begin()
		if err != nil {
			return Prepared{}, err
		}
		return Prepared{Repository: canonical, Roots: roots, Operation: Operation{ID: op.ID, Root: op.Root, DiagnosticsPath: op.DiagnosticsPath, OutputsDir: op.OutputsDir, PrivateDir: op.PrivateDir}}, nil
	}
}

func failResult(state *State, result Result, stage Stage, operationErr error) (Result, error) {
	if err := state.Fail(stage); err != nil {
		return result, errors.Join(operationErr, err)
	}
	syncResultState(&result, state)
	return result, operationErr
}

func incompleteResult(state *State, result Result, stage Stage, operationErr error) (Result, error) {
	if err := state.Incomplete(stage); err != nil {
		return result, errors.Join(operationErr, err)
	}
	syncResultState(&result, state)
	return result, operationErr
}

func incompleteEvidenceResult(ctx context.Context, state *State, core Core, result Result, reason string) (Result, error) {
	_ = core.MarkEvidenceFailed(ctx, result.RunID, reason)
	return incompleteResult(state, result, StageEvidence, ErrIncompleteReview)
}

func syncResultState(result *Result, state *State) {
	result.Status = string(state.Status())
	if stage := state.FailedStage(); stage != "" {
		result.FailedStage = string(stage)
	}
}
func semanticCandidateCount(raw json.RawMessage) (int, error) {
	var v struct {
		Candidates []json.RawMessage `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, err
	}
	return len(v.Candidates), nil
}
func selectedRoles(raw json.RawMessage) ([]string, error) {
	var v struct {
		Selected []struct {
			Role string `json:"role"`
		} `json:"selected"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	roles := make([]string, 0, len(v.Selected))
	seen := map[string]bool{}
	for _, d := range v.Selected {
		if d.Role == "" || seen[d.Role] {
			return nil, errors.New("invalid selected reviewer roles")
		}
		seen[d.Role] = true
		roles = append(roles, d.Role)
	}
	return roles, nil
}
func isCoreValidation(err error) bool {
	var coreErr *CoreError
	return errors.As(err, &coreErr) && coreErr.Kind == CoreErrorValidation
}
func semanticFallbackReason(category diagnostics.Category) string {
	switch category {
	case diagnostics.CategoryAuth:
		return "semantic-routing-auth-failed"
	case diagnostics.CategoryConfig:
		return "semantic-routing-config-failed"
	case diagnostics.CategoryRateLimit:
		return "semantic-routing-rate-limited"
	case diagnostics.CategoryTimeout:
		return "semantic-routing-timeout"
	default:
		return "semantic-routing-process-failed"
	}
}
func reviewerFailureReason(category diagnostics.Category) string {
	if category == diagnostics.CategoryRateLimit {
		return "reviewer-rate-limited"
	}
	if category == diagnostics.CategoryTimeout {
		return "reviewer-timeout"
	}
	return "reviewer-process-failed"
}

func evidenceFailureReason(category diagnostics.Category) string {
	switch category {
	case diagnostics.CategoryAuth:
		return "evidence-auth-failed"
	case diagnostics.CategoryConfig:
		return "evidence-config-failed"
	case diagnostics.CategoryRateLimit:
		return "evidence-rate-limited"
	case diagnostics.CategoryTimeout:
		return "evidence-timeout"
	default:
		return "evidence-process-failed"
	}
}
