package workflow

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/signaturekey/zephyr/internal/gitcontext"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/signaturekey/zephyr/internal/trace"
)

func (service *Service) ValidateRouting(ctx context.Context, options ValidateRoutingOptions) (FinalizeRoutingResult, error) {
	if err := requireService(service); err != nil {
		return FinalizeRoutingResult{}, err
	}
	unlock, err := service.lockRun(ctx, options.RunID)
	if err != nil {
		return FinalizeRoutingResult{}, err
	}
	defer unlock()
	manifest, request, err := service.loadPendingSemanticRouting(ctx, options.RunID)
	if err != nil {
		return FinalizeRoutingResult{}, err
	}
	event, err := service.resumeTrace(manifest, "semantic-routing")
	if err != nil {
		return FinalizeRoutingResult{}, err
	}

	proposal, validationErr := schema.ValidateSemanticRoutingBytes(options.Input)
	if validationErr == nil {
		cfg, configErr := loadEffectiveConfig(service, manifest)
		if configErr != nil {
			_ = event.finish(trace.StatusFailed, configErr)
			return FinalizeRoutingResult{}, configErr
		}
		proposal = sanitizeSemanticRouting(proposal, redactionPolicy(cfg))
	}
	var result routing.Result
	if validationErr == nil {
		result, validationErr = routing.ResolveSemantic(request, proposal)
	}
	if validationErr != nil {
		result, err = routing.FallbackSemantic(request, "invalid semantic routing output")
		if err != nil {
			_ = event.finish(trace.StatusFailed, err)
			return FinalizeRoutingResult{}, err
		}
		finalized, finalizeErr := service.persistFinalRouting(ctx, manifest, result)
		if finalizeErr != nil {
			_ = event.finish(trace.StatusFailed, finalizeErr)
			return FinalizeRoutingResult{}, finalizeErr
		}
		event.setMetadata("validation_status", "invalid")
		event.setMetadata("fallback", "true")
		event.setMetadata("fallback_category", "invalid-output")
		event.setMetadata("selected_roles", fmt.Sprintf("%d", len(result.Selected)))
		return finishCommittedRoutingTrace(finalized, event, trace.StatusPartial, errors.New("invalid semantic routing output; deterministic fallback applied")), nil
	}

	finalized, err := service.persistFinalRouting(ctx, manifest, result)
	if err != nil {
		_ = event.finish(trace.StatusFailed, err)
		return FinalizeRoutingResult{}, err
	}
	event.setMetadata("validation_status", "accepted")
	event.setMetadata("fallback", "false")
	event.setMetadata("selected_roles", fmt.Sprintf("%d", len(result.Selected)))
	return finishCommittedRoutingTrace(finalized, event, trace.StatusCompleted, nil), nil
}

func (service *Service) FallbackRouting(ctx context.Context, options FinalizeRoutingOptions) (FinalizeRoutingResult, error) {
	if err := requireService(service); err != nil {
		return FinalizeRoutingResult{}, err
	}
	if strings.TrimSpace(options.Reason) == "" {
		return FinalizeRoutingResult{}, errors.New("semantic routing fallback reason is required")
	}
	unlock, err := service.lockRun(ctx, options.RunID)
	if err != nil {
		return FinalizeRoutingResult{}, err
	}
	defer unlock()
	manifest, request, err := service.loadPendingSemanticRouting(ctx, options.RunID)
	if err != nil {
		return FinalizeRoutingResult{}, err
	}
	cfg, err := loadEffectiveConfig(service, manifest)
	if err != nil {
		return FinalizeRoutingResult{}, err
	}
	reason := redactionPolicy(cfg).Text(strings.TrimSpace(options.Reason))
	event, err := service.resumeTrace(manifest, "semantic-routing")
	if err != nil {
		return FinalizeRoutingResult{}, err
	}
	result, err := routing.FallbackSemantic(request, reason)
	if err != nil {
		_ = event.finish(trace.StatusFailed, err)
		return FinalizeRoutingResult{}, err
	}
	finalized, err := service.persistFinalRouting(ctx, manifest, result)
	if err != nil {
		_ = event.finish(trace.StatusFailed, err)
		return FinalizeRoutingResult{}, err
	}
	event.setMetadata("validation_status", "process-failure")
	event.setMetadata("fallback", "true")
	event.setMetadata("fallback_category", fallbackCategory(reason))
	event.setMetadata("selected_roles", fmt.Sprintf("%d", len(result.Selected)))
	return finishCommittedRoutingTrace(finalized, event, trace.StatusPartial, errors.New("semantic routing fallback: "+reason)), nil
}

type routingTraceFinisher interface {
	finish(trace.Status, error) error
}

func finishCommittedRoutingTrace(result FinalizeRoutingResult, event routingTraceFinisher, status trace.Status, operationErr error) FinalizeRoutingResult {
	if err := event.finish(status, operationErr); err != nil {
		result.TraceWarning = "routing was committed, but semantic-routing trace finalization failed"
	}
	return result
}

func fallbackCategory(reason string) string {
	value := strings.ToLower(reason)
	switch {
	case strings.Contains(value, "timeout"), strings.Contains(value, "timed out"):
		return "timeout"
	case strings.Contains(value, "auth"):
		return "authentication"
	case strings.Contains(value, "config"):
		return "configuration"
	default:
		return "process-failure"
	}
}

func (service *Service) loadPendingSemanticRouting(ctx context.Context, runID string) (*run.Manifest, routing.SemanticRequest, error) {
	manifest, err := service.store.Load(ctx, runID)
	if err != nil {
		return nil, routing.SemanticRequest{}, err
	}
	if err := ensureStage(manifest, "route", run.StageRunning); err != nil {
		return nil, routing.SemanticRequest{}, fmt.Errorf("semantic routing is not pending: %w", err)
	}
	if err := ensureStage(manifest, "review", run.StagePending); err != nil {
		return nil, routing.SemanticRequest{}, fmt.Errorf("semantic routing cannot change after reviewers start: %w", err)
	}
	snapshot, err := artifact[gitcontext.Snapshot](service, manifest, "git", "snapshot.json")
	if err != nil {
		return nil, routing.SemanticRequest{}, err
	}
	staleness, err := service.collector.CheckStale(ctx, snapshot)
	if err != nil {
		return nil, routing.SemanticRequest{}, err
	}
	if staleness.Stale {
		return nil, routing.SemanticRequest{}, errors.New("Git input changed before semantic routing completed; start a new run")
	}
	request, err := artifact[routing.SemanticRequest](service, manifest, "routing-request.json")
	if err != nil {
		return nil, routing.SemanticRequest{}, err
	}
	if request.RunID != manifest.ID {
		return nil, routing.SemanticRequest{}, errors.New("semantic routing request belongs to another run")
	}
	packetPath, err := service.store.ArtifactPath(manifest.ID, "packet", "review-packet.json")
	if err != nil {
		return nil, routing.SemanticRequest{}, err
	}
	packetBytes, err := os.ReadFile(packetPath)
	if err != nil {
		return nil, routing.SemanticRequest{}, fmt.Errorf("read immutable packet identity: %w", err)
	}
	if actual := fmt.Sprintf("%x", sha256.Sum256(packetBytes)); actual != request.PacketSHA256 {
		return nil, routing.SemanticRequest{}, errors.New("semantic routing request packet identity mismatch")
	}
	return manifest, request, nil
}

func (service *Service) persistFinalRouting(ctx context.Context, manifest *run.Manifest, result routing.Result) (FinalizeRoutingResult, error) {
	path, err := service.store.WriteJSON(ctx, manifest.ID, result, "routing.json")
	if err != nil {
		return FinalizeRoutingResult{}, err
	}
	if err := manifest.SetStage("route", run.StageComplete, service.now(), ""); err != nil {
		return FinalizeRoutingResult{}, err
	}
	manifest.State = run.StateRunning
	if err := service.store.Save(ctx, manifest); err != nil {
		return FinalizeRoutingResult{}, err
	}
	return FinalizeRoutingResult{RunID: manifest.ID, RoutingPath: path, Routing: result}, nil
}
