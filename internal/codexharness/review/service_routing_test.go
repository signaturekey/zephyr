package review

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/signaturekey/zephyr/internal/codexharness/compatibility"
	"github.com/signaturekey/zephyr/internal/codexharness/dispatch"
	"github.com/signaturekey/zephyr/internal/codexharness/preflight"
)

func TestService_RoutingSkipsSemanticDispatcherWhenThereAreNoCandidates(t *testing.T) {
	core := newServiceFakeCore()
	core.route = RouteResult{RunID: "run-abcd", PacketPath: "/packet.json", RoutingRequestPath: "/routing-request.json",
		RoutingRequest: json.RawMessage(`{"candidates":[]}`), Routing: json.RawMessage(`{"selected":[]}`)}
	dispatcher := &serviceFakeDispatcher{}
	service := NewService(Dependencies{
		Prepare: fakePreparation(t), Preflight: serviceFakePreflight{}, Core: core,
		Dispatcher: dispatcher, Compatibility: serviceFakeCompatibility{}, Scheduler: inlineScheduler{},
	})
	result, err := service.Review(context.Background(), ReviewOptions{Repository: t.TempDir()})
	if err == nil || result.Status != string(StatusIncomplete) {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if dispatcher.routeCalls != 0 {
		t.Fatalf("semantic dispatcher calls=%d, want 0", dispatcher.routeCalls)
	}
}

type serviceFakePreflight struct{}

func (serviceFakePreflight) Check(context.Context) (preflight.Result, error) {
	return preflight.Result{}, nil
}

type serviceFakeCompatibility struct{}

func (serviceFakeCompatibility) Ensure(context.Context, string, string, string) (compatibility.Result, error) {
	return compatibility.Result{DescriptorPath: "/compat.txt"}, nil
}

type serviceFakeDispatcher struct {
	routeCalls, reviewCalls, evidenceCalls int
	evidenceErr                            error
}

func (*serviceFakeDispatcher) Probe(context.Context, dispatch.ProbeRequest) (dispatch.Result, error) {
	return dispatch.Result{}, nil
}
func (*serviceFakeDispatcher) Smoke(context.Context, dispatch.Common) (dispatch.Result, error) {
	return dispatch.Result{}, nil
}
func (d *serviceFakeDispatcher) Route(context.Context, dispatch.RoutingRequest) (dispatch.Result, error) {
	d.routeCalls++
	return dispatch.Result{}, nil
}
func (d *serviceFakeDispatcher) Review(context.Context, dispatch.ReviewerRequest) (dispatch.Result, error) {
	d.reviewCalls++
	return dispatch.Result{}, nil
}
func (d *serviceFakeDispatcher) Evidence(context.Context, dispatch.EvidenceRequest) (dispatch.Result, error) {
	d.evidenceCalls++
	return dispatch.Result{}, d.evidenceErr
}
