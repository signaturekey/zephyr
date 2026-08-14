package review

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/signaturekey/zephyr/internal/codexharness/layout"
	"github.com/signaturekey/zephyr/internal/codexharness/scheduler"
)

func TestService_ReviewersRetriesFormatOnce(t *testing.T) {
	core := newServiceFakeCore()
	core.route = RouteResult{RunID: "run-abcd", PacketPath: "/packet.json", RoutingRequest: json.RawMessage(`{"candidates":[]}`), Routing: json.RawMessage(`{"selected":[{"role":"code-reviewer"}]}`)}
	core.validationErrors = []error{&CoreError{Kind: CoreErrorValidation, Op: "validate-candidates"}, nil}
	dispatcher := &serviceFakeDispatcher{}
	service := NewService(Dependencies{Prepare: fakePreparation(t), Preflight: serviceFakePreflight{}, Core: core, Dispatcher: dispatcher, Compatibility: serviceFakeCompatibility{}, Scheduler: inlineScheduler{}})
	_, _ = service.Review(context.Background(), ReviewOptions{Repository: t.TempDir()})
	if dispatcher.reviewCalls != 2 {
		t.Fatalf("review calls=%d, want 2", dispatcher.reviewCalls)
	}
}

type inlineScheduler struct{}

func (inlineScheduler) Run(ctx context.Context, jobs []scheduler.Job, _ time.Duration) []scheduler.Result {
	results := make([]scheduler.Result, len(jobs))
	for i, job := range jobs {
		results[i] = job.Run(ctx)
	}
	return results
}

type serviceFakeCore struct {
	route            RouteResult
	validationErrors []error
	evidenceErr      error
	verdictErr       error
	aggregateErr     error
	renderErr        error
	calls            []string
}

func newServiceFakeCore() *serviceFakeCore                            { return &serviceFakeCore{} }
func (*serviceFakeCore) Version(context.Context) (CoreVersion, error) { return CoreVersion{}, nil }
func (*serviceFakeCore) Init(context.Context, string) (InitResult, error) {
	return InitResult{RunID: "run-abcd"}, nil
}
func (*serviceFakeCore) Collect(context.Context, string) (CollectResult, error) {
	return CollectResult{Reviewable: true, ModelPolicyPath: "/policy.txt"}, nil
}
func (*serviceFakeCore) SetCapability(context.Context, string, string) error  { return nil }
func (f *serviceFakeCore) Route(context.Context, string) (RouteResult, error) { return f.route, nil }
func (*serviceFakeCore) ValidateRouting(context.Context, string, string) (FinalizeRoutingResult, error) {
	return FinalizeRoutingResult{}, nil
}
func (*serviceFakeCore) FallbackRouting(context.Context, string, string) (FinalizeRoutingResult, error) {
	return FinalizeRoutingResult{}, nil
}
func (f *serviceFakeCore) ValidateCandidates(context.Context, string, string, string) (ValidateCandidatesResult, error) {
	if len(f.validationErrors) > 0 {
		e := f.validationErrors[0]
		f.validationErrors = f.validationErrors[1:]
		return ValidateCandidatesResult{}, e
	}
	return ValidateCandidatesResult{}, nil
}
func (*serviceFakeCore) MarkReviewerFailed(context.Context, string, string, string) error { return nil }
func (f *serviceFakeCore) PrepareEvidence(context.Context, string) (PrepareEvidenceResult, error) {
	f.calls = append(f.calls, "prepare-evidence")
	return PrepareEvidenceResult{CandidateSet: "/prechecked.json", Evidence: "/minimal.json"}, f.evidenceErr
}
func (f *serviceFakeCore) ValidateVerdicts(context.Context, string, string) (ValidateVerdictsResult, error) {
	f.calls = append(f.calls, "validate-verdicts")
	return ValidateVerdictsResult{}, f.verdictErr
}
func (f *serviceFakeCore) MarkEvidenceFailed(context.Context, string, string) error {
	f.calls = append(f.calls, "mark-evidence-failed")
	return nil
}
func (f *serviceFakeCore) Aggregate(context.Context, string) (AggregateResult, error) {
	f.calls = append(f.calls, "aggregate")
	return AggregateResult{}, f.aggregateErr
}
func (f *serviceFakeCore) Render(context.Context, string) (RenderResult, error) {
	f.calls = append(f.calls, "render")
	return RenderResult{}, f.renderErr
}
func (f *serviceFakeCore) Inspect(context.Context, string) (InspectResult, error) {
	f.calls = append(f.calls, "inspect")
	return InspectResult{}, nil
}

func fakePreparation(t *testing.T) PrepareFunc {
	t.Helper()
	root := t.TempDir()
	return func(string, bool) (Prepared, error) {
		return Prepared{Repository: root, Roots: layout.Roots{DriverRoot: root, Operation: root + "/operations", RunRoot: root + "/runs", CacheRoot: root + "/cache"}, Operation: Operation{ID: "op", Root: root, OutputsDir: root, PrivateDir: root, DiagnosticsPath: root + "/diagnostics.json"}}, nil
	}
}
