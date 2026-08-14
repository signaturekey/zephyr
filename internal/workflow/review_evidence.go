package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/gitcontext"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/signaturekey/zephyr/internal/trace"
)

func (service *Service) ValidateCandidates(ctx context.Context, options ValidateCandidatesOptions) (result ValidateCandidatesResult, returnErr error) {
	if err := requireService(service); err != nil {
		return result, err
	}
	unlock, err := service.lockRun(ctx, options.RunID)
	if err != nil {
		return result, err
	}
	defer unlock()
	if len(options.Input) == 0 {
		return result, errors.New("candidate input is empty")
	}
	if int64(len(options.Input)) > maxAgentJSONBytes {
		return result, fmt.Errorf("candidate input exceeds %d bytes", maxAgentJSONBytes)
	}
	manifest, err := service.store.Load(ctx, options.RunID)
	if err != nil {
		return result, err
	}
	if err := ensureStage(manifest, "route", run.StageComplete); err != nil {
		return result, fmt.Errorf("validate candidates: %w", err)
	}
	if err := ensureStage(manifest, "evidence", run.StagePending); err != nil {
		return result, fmt.Errorf("validate candidates: evidence gate already started: %w", err)
	}
	routeResult, err := artifact[routing.Result](service, manifest, "routing.json")
	if err != nil {
		return result, err
	}
	options.Role = strings.TrimSpace(options.Role)
	if !selectedRole(routeResult, options.Role) {
		return result, fmt.Errorf("role %q was not selected for run %q", options.Role, manifest.ID)
	}
	envelope, err := schema.ValidateCandidateBytes(options.Input)
	if err != nil {
		return result, err
	}
	if envelope.RunID != manifest.ID {
		return result, fmt.Errorf("candidate run_id %q does not match run %q", envelope.RunID, manifest.ID)
	}
	if envelope.Role != options.Role {
		return result, fmt.Errorf("candidate role %q does not match --role %q", envelope.Role, options.Role)
	}
	cfg, err := artifact[config.Config](service, manifest, "context", "config.json")
	if err != nil {
		return result, err
	}
	envelope = sanitizeCandidateEnvelope(envelope, redactionPolicy(cfg))
	snapshot, err := artifact[gitcontext.Snapshot](service, manifest, "git", "snapshot.json")
	if err != nil {
		return result, err
	}
	stale, err := service.collector.CheckStale(ctx, snapshot)
	if err != nil {
		return result, err
	}
	if stale.Stale {
		return result, errors.New("Git input changed after routing; discard this output and start a new run")
	}
	accounted, err := service.roleAccounted(manifest, options.Role)
	if err != nil {
		return result, err
	}
	if accounted {
		precheckPath, pathErr := service.store.ArtifactPath(manifest.ID, "evidence", "precheck", options.Role+".json")
		if pathErr != nil {
			return result, pathErr
		}
		if _, statErr := os.Stat(precheckPath); statErr == nil {
			candidatePath, pathErr := service.store.ArtifactPath(manifest.ID, "candidates", options.Role+".json")
			if pathErr != nil {
				return result, pathErr
			}
			stored, decodeErr := decodeStrict[schema.CandidateEnvelope](candidatePath)
			if decodeErr != nil {
				return result, fmt.Errorf("recover role %q: %w", options.Role, decodeErr)
			}
			if !reflect.DeepEqual(stored, envelope) {
				return result, fmt.Errorf("role %q already has a different validated result", options.Role)
			}
			precheck, decodeErr := decodeStrict[evidence.PrecheckReport](precheckPath)
			if decodeErr != nil {
				return result, fmt.Errorf("recover role %q precheck: %w", options.Role, decodeErr)
			}
			candidateSetPath, rebuildErr := service.rebuildCandidateArtifacts(ctx, manifest, routeResult)
			if rebuildErr != nil {
				return result, rebuildErr
			}
			complete, accountErr := service.reviewAccounted(manifest, routeResult)
			if accountErr != nil {
				return result, accountErr
			}
			if complete {
				if stageErr := manifest.SetStage("review", run.StageComplete, service.now(), ""); stageErr != nil {
					return result, stageErr
				}
			}
			if saveErr := service.store.Save(ctx, manifest); saveErr != nil {
				return result, saveErr
			}
			return ValidateCandidatesResult{
				RunID: manifest.ID, Role: options.Role, Accepted: len(precheck.Accepted), Rejected: len(precheck.Rejected),
				CandidatePath: candidatePath, PrecheckPath: precheckPath, CandidateSetPath: candidateSetPath,
			}, nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return result, statErr
		}
		return result, fmt.Errorf("role %q already has a recorded failure", options.Role)
	}
	if err := manifest.SetStage("review", run.StageRunning, service.now(), ""); err != nil {
		return result, err
	}
	manifest.State = run.StateRunning
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	event, err := service.startTrace(manifest, "validate-candidates", map[string]string{"role": options.Role})
	if err != nil {
		return result, err
	}
	defer func() {
		status := trace.StatusCompleted
		if returnErr != nil {
			status = trace.StatusFailed
		}
		if err := event.finish(status, returnErr); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	packet, err := artifact[contextpack.Packet](service, manifest, "packet", "review-packet.json")
	if err != nil {
		return result, err
	}
	precheck := evidence.Precheck(envelope, packet, cfg)
	stale, err = service.collector.CheckStale(ctx, snapshot)
	if err != nil {
		return result, err
	}
	if stale.Stale {
		return result, errors.New("Git input changed during candidate validation; start a new run")
	}
	candidatePath, err := service.store.WriteJSON(ctx, manifest.ID, envelope, "candidates", options.Role+".json")
	if err != nil {
		return result, err
	}
	precheckPath, err := service.store.WriteJSON(ctx, manifest.ID, precheck, "evidence", "precheck", options.Role+".json")
	if err != nil {
		return result, err
	}
	candidateSetPath, err := service.rebuildCandidateArtifacts(ctx, manifest, routeResult)
	if err != nil {
		return result, err
	}
	complete, err := service.reviewAccounted(manifest, routeResult)
	if err != nil {
		return result, err
	}
	if complete {
		if err := manifest.SetStage("review", run.StageComplete, service.now(), ""); err != nil {
			return result, err
		}
	}
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	return ValidateCandidatesResult{
		RunID:            manifest.ID,
		Role:             options.Role,
		Accepted:         len(precheck.Accepted),
		Rejected:         len(precheck.Rejected),
		CandidatePath:    candidatePath,
		PrecheckPath:     precheckPath,
		CandidateSetPath: candidateSetPath,
	}, nil
}

func (service *Service) ValidateVerdicts(ctx context.Context, options ValidateVerdictsOptions) (result ValidateVerdictsResult, returnErr error) {
	if err := requireService(service); err != nil {
		return result, err
	}
	unlock, err := service.lockRun(ctx, options.RunID)
	if err != nil {
		return result, err
	}
	defer unlock()
	if len(options.Input) == 0 {
		return result, errors.New("verdict input is empty")
	}
	if int64(len(options.Input)) > maxAgentJSONBytes {
		return result, fmt.Errorf("verdict input exceeds %d bytes", maxAgentJSONBytes)
	}
	manifest, err := service.store.Load(ctx, options.RunID)
	if err != nil {
		return result, err
	}
	if err := ensureStage(manifest, "review", run.StageComplete); err != nil {
		return result, fmt.Errorf("validate verdicts: all selected roles must be validated or marked failed: %w", err)
	}
	if err := ensureStage(manifest, "evidence", run.StagePending); err != nil {
		return result, fmt.Errorf("validate verdicts: %w", err)
	}
	if err := manifest.SetStage("evidence", run.StageRunning, service.now(), ""); err != nil {
		return result, err
	}
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	event, err := service.startTrace(manifest, "validate-verdicts", nil)
	if err != nil {
		return result, err
	}
	defer func() {
		status := trace.StatusCompleted
		if returnErr != nil {
			status = trace.StatusFailed
			_ = manifest.SetStage("evidence", run.StageFailed, service.now(), safeError(returnErr))
			manifest.State = run.StateIncomplete
			appendUnique(&manifest.CoverageLimits, "evidence-gate: "+safeError(returnErr))
			_ = service.store.Save(context.Background(), manifest)
		}
		if err := event.finish(status, returnErr); returnErr == nil && err != nil {
			returnErr = err
		}
	}()
	routeResult, err := artifact[routing.Result](service, manifest, "routing.json")
	if err != nil {
		return result, err
	}
	precheckReports, err := loadPrecheckReports(service, manifest, routeResult)
	if err != nil {
		return result, err
	}
	if len(routeResult.Selected) > 0 && len(precheckReports) == 0 {
		return result, errors.New("no selected reviewer produced a validated result")
	}

	verdicts, err := schema.ValidateVerdictBytes(options.Input)
	if err != nil {
		return result, err
	}
	if verdicts.RunID != manifest.ID {
		return result, fmt.Errorf("verdict run_id %q does not match run %q", verdicts.RunID, manifest.ID)
	}
	cfg, err := artifact[config.Config](service, manifest, "context", "config.json")
	if err != nil {
		return result, err
	}
	verdicts = sanitizeVerdicts(verdicts, redactionPolicy(cfg))
	candidates, err := artifact[evidence.CandidateSet](service, manifest, "evidence", "prechecked.json")
	if err != nil {
		return result, err
	}
	if err := evidence.ValidateVerdicts(verdicts, candidates); err != nil {
		return result, err
	}
	verdictPath, err := service.store.WriteJSON(ctx, manifest.ID, verdicts, "evidence", "verdicts.json")
	if err != nil {
		return result, err
	}
	if err := manifest.SetStage("evidence", run.StageComplete, service.now(), ""); err != nil {
		return result, err
	}
	manifest.State = run.StateRunning
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	return ValidateVerdictsResult{RunID: manifest.ID, Verdicts: len(verdicts.Verdicts), VerdictPath: verdictPath}, nil
}

func (service *Service) PrepareEvidence(ctx context.Context, options PrepareEvidenceOptions) (PrepareEvidenceResult, error) {
	if err := requireService(service); err != nil {
		return PrepareEvidenceResult{}, err
	}
	unlock, err := service.lockRun(ctx, options.RunID)
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	defer unlock()
	manifest, err := service.store.Load(ctx, options.RunID)
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	if err := ensureStage(manifest, "route", run.StageComplete); err != nil {
		return PrepareEvidenceResult{}, err
	}
	if err := ensureStage(manifest, "review", run.StageComplete); err != nil {
		return PrepareEvidenceResult{}, err
	}
	if err := ensureStage(manifest, "evidence", run.StagePending); err != nil {
		return PrepareEvidenceResult{}, err
	}
	routeResult, err := artifact[routing.Result](service, manifest, "routing.json")
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	reports, err := loadPrecheckReports(service, manifest, routeResult)
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	if len(reports) == 0 {
		return PrepareEvidenceResult{}, errors.New("no selected reviewer produced a validated result")
	}
	packet, err := artifact[contextpack.Packet](service, manifest, "packet", "review-packet.json")
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	candidates, err := artifact[evidence.CandidateSet](service, manifest, "evidence", "prechecked.json")
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	if candidates.RunID != manifest.ID || packet.RunID != manifest.ID {
		return PrepareEvidenceResult{}, fmt.Errorf("packet and prechecked candidates must match run %q", manifest.ID)
	}
	snapshot, err := artifact[gitcontext.Snapshot](service, manifest, "git", "snapshot.json")
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	stale, err := service.collector.CheckStale(ctx, snapshot)
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	if stale.Stale {
		return PrepareEvidenceResult{}, errors.New("Git input changed before evidence preparation; start a new run")
	}
	input, err := evidence.BuildGateInput(candidates, packet)
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	stale, err = service.collector.CheckStale(ctx, snapshot)
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	if stale.Stale {
		return PrepareEvidenceResult{}, errors.New("Git input changed during evidence preparation; start a new run")
	}
	candidatePath, err := service.store.ArtifactPath(manifest.ID, "evidence", "prechecked.json")
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	evidencePath, err := service.store.WriteJSON(ctx, manifest.ID, input, "evidence", "minimal.json")
	if err != nil {
		return PrepareEvidenceResult{}, err
	}
	return PrepareEvidenceResult{RunID: manifest.ID, CandidateSet: candidatePath, Evidence: evidencePath, Items: len(input.Items)}, nil
}

func (service *Service) MarkFailed(ctx context.Context, options MarkFailedOptions) (result MarkFailedResult, returnErr error) {
	if err := requireService(service); err != nil {
		return result, err
	}
	unlock, err := service.lockRun(ctx, options.RunID)
	if err != nil {
		return result, err
	}
	defer unlock()
	options.Stage = strings.TrimSpace(options.Stage)
	options.Role = strings.TrimSpace(options.Role)
	options.Reason = strings.TrimSpace(options.Reason)
	if options.Reason == "" {
		return result, errors.New("failure reason is required")
	}
	manifest, err := service.store.Load(ctx, options.RunID)
	if err != nil {
		return result, err
	}
	event, err := service.startTrace(manifest, "mark-failed", map[string]string{"stage": options.Stage, "role": options.Role})
	if err != nil {
		return result, err
	}
	defer func() {
		status := trace.StatusPartial
		if returnErr != nil || options.Stage == "evidence" {
			status = trace.StatusFailed
		}
		if err := event.finish(status, returnErr); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	switch options.Stage {
	case "review":
		if options.Role == "" {
			return result, errors.New("mark review failed: --role is required")
		}
		if err := ensureStage(manifest, "route", run.StageComplete); err != nil {
			return result, err
		}
		if err := ensureStage(manifest, "evidence", run.StagePending); err != nil {
			return result, fmt.Errorf("cannot mark reviewer after evidence starts: %w", err)
		}
		routeResult, err := artifact[routing.Result](service, manifest, "routing.json")
		if err != nil {
			return result, err
		}
		if !selectedRole(routeResult, options.Role) {
			return result, fmt.Errorf("role %q was not selected", options.Role)
		}
		precheckPath, err := service.store.ArtifactPath(manifest.ID, "evidence", "precheck", options.Role+".json")
		if err != nil {
			return result, err
		}
		if _, statErr := os.Stat(precheckPath); statErr == nil {
			return result, fmt.Errorf("role %q already has a validated result", options.Role)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return result, statErr
		}
		cfg, err := artifact[config.Config](service, manifest, "context", "config.json")
		if err != nil {
			return result, err
		}
		expectedReason := redactionPolicy(cfg).Text(options.Reason)
		recordedReason, recorded, err := service.recordedCoverage(manifest, "reviewer:"+options.Role)
		if err != nil {
			return result, err
		}
		if recorded && recordedReason != expectedReason {
			return result, fmt.Errorf("role %q already has a different recorded failure", options.Role)
		}
		if !recorded {
			if _, err := service.addCoverage(ctx, CoverageAddOptions{
				RunID: manifest.ID, Source: "reviewer:" + options.Role, Reason: options.Reason, AllowAfterRoute: true,
			}); err != nil {
				return result, err
			}
		}
		manifest, err = service.store.Load(ctx, manifest.ID)
		if err != nil {
			return result, err
		}
		if err := manifest.SetStage("review", run.StageRunning, service.now(), ""); err != nil {
			return result, err
		}
		if _, err := service.rebuildCandidateArtifacts(ctx, manifest, routeResult); err != nil {
			return result, err
		}
		complete, err := service.reviewAccounted(manifest, routeResult)
		if err != nil {
			return result, err
		}
		if complete {
			if err := manifest.SetStage("review", run.StageComplete, service.now(), ""); err != nil {
				return result, err
			}
		}
		manifest.State = run.StateRunning
	case "evidence":
		if options.Role != "" {
			return result, errors.New("mark evidence failed: --role is not allowed")
		}
		if err := ensureStage(manifest, "review", run.StageComplete); err != nil {
			return result, fmt.Errorf("mark evidence failed: reviewers are not complete: %w", err)
		}
		cfg, err := artifact[config.Config](service, manifest, "context", "config.json")
		if err != nil {
			return result, err
		}
		expectedReason := redactionPolicy(cfg).Text(options.Reason)
		recordedReason, recorded, err := service.recordedCoverage(manifest, "evidence-gate")
		if err != nil {
			return result, err
		}
		state, _ := stageState(manifest, "evidence")
		if state == run.StageFailed && recorded && recordedReason == expectedReason {
			return MarkFailedResult{RunID: manifest.ID, State: manifest.State, Stage: options.Stage}, nil
		}
		if err := ensureStage(manifest, "evidence", run.StagePending); err != nil {
			return result, fmt.Errorf("mark evidence failed: %w", err)
		}
		if recorded && recordedReason != expectedReason {
			return result, errors.New("evidence gate already has a different recorded failure")
		}
		if !recorded {
			if _, err := service.addCoverage(ctx, CoverageAddOptions{
				RunID: manifest.ID, Source: "evidence-gate", Reason: options.Reason, AllowAfterRoute: true,
			}); err != nil {
				return result, err
			}
		}
		manifest, err = service.store.Load(ctx, manifest.ID)
		if err != nil {
			return result, err
		}
		if err := manifest.SetStage("evidence", run.StageFailed, service.now(), safeError(errors.New(options.Reason))); err != nil {
			return result, err
		}
		manifest.State = run.StateIncomplete
	default:
		return result, fmt.Errorf("unsupported failed stage %q; expected review or evidence", options.Stage)
	}
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	return MarkFailedResult{RunID: manifest.ID, State: manifest.State, Stage: options.Stage, Role: options.Role}, nil
}

func (service *Service) recordedCoverage(manifest *run.Manifest, source string) (string, bool, error) {
	doc, err := loadCoverage(service, manifest)
	if err != nil {
		return "", false, err
	}
	for _, limit := range doc.Limits {
		if limit.Source == source {
			return limit.Reason, true, nil
		}
	}
	return "", false, nil
}

func (service *Service) reviewAccounted(manifest *run.Manifest, routeResult routing.Result) (bool, error) {
	reports, err := loadPrecheckReports(service, manifest, routeResult)
	if err != nil {
		return false, err
	}
	covered := make(map[string]struct{}, len(reports))
	for _, report := range reports {
		covered[report.Role] = struct{}{}
	}
	limits, err := loadCoverage(service, manifest)
	if err != nil {
		return false, err
	}
	for _, limit := range limits.Limits {
		if strings.HasPrefix(limit.Source, "reviewer:") {
			covered[strings.TrimPrefix(limit.Source, "reviewer:")] = struct{}{}
		}
	}
	for _, decision := range routeResult.Selected {
		if _, ok := covered[decision.Role]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func (service *Service) roleAccounted(manifest *run.Manifest, role string) (bool, error) {
	path, err := service.store.ArtifactPath(manifest.ID, "evidence", "precheck", role+".json")
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	limits, err := loadCoverage(service, manifest)
	if err != nil {
		return false, err
	}
	for _, limit := range limits.Limits {
		if limit.Source == "reviewer:"+role {
			return true, nil
		}
	}
	return false, nil
}

func (service *Service) rebuildCandidateArtifacts(ctx context.Context, manifest *run.Manifest, routeResult routing.Result) (string, error) {
	reports, err := loadPrecheckReports(service, manifest, routeResult)
	if err != nil {
		return "", err
	}
	candidateSet := evidence.MergeCandidateReports(manifest.ID, reports)
	path, err := service.store.WriteJSON(ctx, manifest.ID, candidateSet, "evidence", "prechecked.json")
	if err != nil {
		return "", err
	}
	if _, err := service.store.WriteJSON(ctx, manifest.ID, evidence.MergeRejections(manifest.ID, reports), "evidence", "precheck.json"); err != nil {
		return "", err
	}
	return path, nil
}
