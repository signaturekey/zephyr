package workflow

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/gitcontext"
	"github.com/signaturekey/zephyr/internal/report"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/signaturekey/zephyr/internal/trace"
)

func (service *Service) Aggregate(ctx context.Context, runID string) (result AggregateResult, returnErr error) {
	if err := requireService(service); err != nil {
		return result, err
	}
	unlock, err := service.lockRun(ctx, runID)
	if err != nil {
		return result, err
	}
	defer unlock()
	manifest, err := service.store.Load(ctx, runID)
	if err != nil {
		return result, err
	}
	if err := ensureStage(manifest, "evidence", run.StageComplete); err != nil {
		return result, fmt.Errorf("aggregate: evidence gate must complete successfully: %w", err)
	}
	if err := ensureStage(manifest, "aggregate", run.StagePending, run.StageFailed); err != nil {
		return result, err
	}
	if err := manifest.SetStage("aggregate", run.StageRunning, service.now(), ""); err != nil {
		return result, err
	}
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	event, err := service.startTrace(manifest, "aggregate", nil)
	if err != nil {
		return result, err
	}
	defer func() {
		status := trace.StatusCompleted
		if returnErr != nil {
			status = trace.StatusFailed
			_ = manifest.SetStage("aggregate", run.StageFailed, service.now(), safeError(returnErr))
			manifest.State = run.StateFailed
			_ = service.store.Save(context.Background(), manifest)
		}
		if err := event.finish(status, returnErr); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	packet, err := artifact[contextpack.Packet](service, manifest, "packet", "review-packet.json")
	if err != nil {
		return result, err
	}
	snapshot, err := artifact[gitcontext.Snapshot](service, manifest, "git", "snapshot.json")
	if err != nil {
		return result, err
	}
	routeResult, err := artifact[routing.Result](service, manifest, "routing.json")
	if err != nil {
		return result, err
	}
	cfg, err := artifact[config.Config](service, manifest, "context", "config.json")
	if err != nil {
		return result, err
	}
	modelPolicyPath, err := service.store.ArtifactPath(manifest.ID, "context", "model-policy.txt")
	if err != nil {
		return result, err
	}
	modelPolicyBytes, err := os.ReadFile(modelPolicyPath)
	if err != nil {
		return result, fmt.Errorf("read frozen model policy: %w", err)
	}
	modelPolicySHA256 := fmt.Sprintf("%x", sha256.Sum256(modelPolicyBytes))
	candidates, err := artifact[evidence.CandidateSet](service, manifest, "evidence", "prechecked.json")
	if err != nil {
		return result, err
	}
	verdicts, err := artifact[schema.EvidenceVerdictEnvelope](service, manifest, "evidence", "verdicts.json")
	if err != nil {
		return result, err
	}
	reports, err := loadPrecheckReports(service, manifest, routeResult)
	if err != nil {
		return result, err
	}
	staleness, err := service.collector.CheckStale(ctx, snapshot)
	if err != nil {
		return result, err
	}
	if _, err := service.store.WriteJSON(ctx, manifest.ID, staleness, "git", "staleness.json"); err != nil {
		return result, err
	}
	coverage := append([]string(nil), manifest.CoverageLimits...)
	coverage = append(coverage, packet.CoverageLimits...)
	coverageDoc, err := loadCoverage(service, manifest)
	if err != nil {
		return result, err
	}
	for _, limit := range coverageDoc.Limits {
		coverage = append(coverage, limit.Source+": "+limit.Reason)
	}
	if staleness.Stale {
		message := "Git snapshot became stale after collection; the report refers only to the original snapshot"
		coverage = append(coverage, message)
		appendUnique(&manifest.CoverageLimits, message)
	}
	rejectedPath, err := service.store.ArtifactPath(manifest.ID, "rejected-findings.json")
	if err != nil {
		return result, err
	}
	provenance := make([]report.SourceProvenance, 0, len(packet.BusinessContext)+len(packet.ProjectInstructions)+1)
	if packet.Plan != nil {
		provenance = append(provenance, report.SourceProvenance{Source: "plan", Key: packet.Plan.Path, ContentHash: packet.Plan.ContentHash})
	}
	for _, source := range packet.BusinessContext {
		provenance = append(provenance, report.SourceProvenance{
			Source: source.Source, Key: source.Key, URL: source.URL, ContentHash: source.ContentHash, FetchedAt: source.FetchedAt,
		})
	}
	for _, source := range packet.ProjectInstructions {
		provenance = append(provenance, report.SourceProvenance{Source: "project-instruction", Key: source.Path, ContentHash: source.ContentHash})
	}
	review, rejected, err := report.Aggregate(report.AggregateInput{
		RunID:       manifest.ID,
		GeneratedAt: service.now(),
		Scope: report.Scope{
			Mode:         string(manifest.Mode),
			Source:       string(manifest.Source),
			Repository:   packet.Repository.Root,
			Branch:       packet.Repository.Branch,
			Head:         packet.Repository.Head,
			BaseRef:      packet.Repository.BaseRef,
			BaseSHA:      packet.Repository.BaseSHA,
			TargetSHA:    packet.Repository.TargetSHA,
			MergeBase:    packet.Repository.MergeBase,
			CommitRange:  packet.Repository.CommitRange,
			ChangedFiles: append([]string{}, packet.ChangedFiles...),
			Plan: func() string {
				if packet.Plan != nil {
					return packet.Plan.Path
				}
				return ""
			}(),
			Sources:           provenance,
			Stale:             staleness.Stale,
			ModelPolicySHA256: modelPolicySHA256,
		},
		Routing:         reportRouting(routeResult, cfg),
		Candidates:      candidates,
		Verdicts:        verdicts,
		PrecheckReports: reports,
		CoverageLimits:  coverage,
		RejectedPath:    rejectedPath,
	})
	if packet.Plan != nil {
		review.Scope.PlanHash = packet.Plan.ContentHash
	}
	if err != nil {
		return result, err
	}
	reviewPath, err := service.store.WriteJSON(ctx, manifest.ID, review, "review.json")
	if err != nil {
		return result, err
	}
	if _, err := service.store.WriteJSON(ctx, manifest.ID, rejected, "rejected-findings.json"); err != nil {
		return result, err
	}
	if err := manifest.SetStage("aggregate", run.StageComplete, service.now(), ""); err != nil {
		return result, err
	}
	manifest.State = run.StateRunning
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	return AggregateResult{
		RunID:        manifest.ID,
		Status:       review.Status,
		Findings:     len(review.Findings),
		NeedsHuman:   len(review.NeedsHuman),
		Stale:        staleness.Stale,
		ReviewPath:   reviewPath,
		RejectedPath: rejectedPath,
	}, nil
}

func (service *Service) Render(ctx context.Context, runID string, includeP3 bool) (result RenderResult, returnErr error) {
	if err := requireService(service); err != nil {
		return result, err
	}
	unlock, err := service.lockRun(ctx, runID)
	if err != nil {
		return result, err
	}
	defer unlock()
	manifest, err := service.store.Load(ctx, runID)
	if err != nil {
		return result, err
	}
	if err := ensureStage(manifest, "aggregate", run.StageComplete); err != nil {
		return result, fmt.Errorf("render: %w", err)
	}
	if err := ensureStage(manifest, "render", run.StagePending, run.StageFailed); err != nil {
		return result, err
	}
	if err := manifest.SetStage("render", run.StageRunning, service.now(), ""); err != nil {
		return result, err
	}
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	event, err := service.startTrace(manifest, "render", nil)
	if err != nil {
		return result, err
	}
	defer func() {
		status := trace.StatusCompleted
		if returnErr != nil {
			status = trace.StatusFailed
			_ = manifest.SetStage("render", run.StageFailed, service.now(), safeError(returnErr))
			manifest.State = run.StateFailed
			_ = service.store.Save(context.Background(), manifest)
		}
		if err := event.finish(status, returnErr); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	review, err := artifact[report.Review](service, manifest, "review.json")
	if err != nil {
		return result, err
	}
	cfg, err := artifact[config.Config](service, manifest, "context", "config.json")
	if err != nil {
		return result, err
	}
	markdown, err := report.RenderMarkdown(review, report.RenderOptions{
		IncludeP3: includeP3, MaxFinalFindings: cfg.Limits.MaxFinalFindings,
	})
	if err != nil {
		return result, err
	}
	markdownPath, err := service.store.WriteArtifact(ctx, manifest.ID, markdown, "review.md")
	if err != nil {
		return result, err
	}
	jsonPath, err := service.store.ArtifactPath(manifest.ID, "review.json")
	if err != nil {
		return result, err
	}
	if err := manifest.SetStage("render", run.StageComplete, service.now(), ""); err != nil {
		return result, err
	}
	manifest.State = run.StateComplete
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	return RenderResult{RunID: manifest.ID, Status: review.Status, ReviewMD: markdownPath, ReviewJSON: jsonPath}, nil
}

func reportRouting(routeResult routing.Result, cfg config.Config) report.RoutingSummary {
	convert := func(decisions []routing.Decision) []report.RoleDecision {
		result := make([]report.RoleDecision, 0, len(decisions))
		for _, decision := range decisions {
			reasons := make([]string, 0, len(decision.Reasons))
			for _, reason := range decision.Reasons {
				value := reason.Code
				if reason.Detail != "" {
					value += ": " + reason.Detail
				}
				reasons = append(reasons, value)
			}
			result = append(result, report.RoleDecision{Role: decision.Role, Reasons: reasons})
		}
		return result
	}
	return report.RoutingSummary{
		Profile:     string(routeResult.Profile),
		Selected:    convert(routeResult.Selected),
		Excluded:    convert(routeResult.Excluded),
		MaxParallel: cfg.Limits.MaxParallelReviewers,
	}
}

func (service *Service) Inspect(ctx context.Context, runID string) (InspectResult, error) {
	if err := requireService(service); err != nil {
		return InspectResult{}, err
	}
	unlock, err := service.lockRun(ctx, runID)
	if err != nil {
		return InspectResult{}, err
	}
	defer unlock()
	manifest, err := service.store.Load(ctx, runID)
	if err != nil {
		return InspectResult{}, err
	}
	result := InspectResult{
		RunID:          manifest.ID,
		RunDir:         manifest.RunDir,
		State:          manifest.State,
		Mode:           manifest.Mode,
		Source:         manifest.Source,
		Stages:         append([]run.Stage(nil), manifest.Stages...),
		CoverageLimits: append([]string(nil), manifest.CoverageLimits...),
		Counts: InspectCounts{
			BySeverity: map[string]int{},
		},
	}
	result.Artifacts.Manifest, _ = service.store.ArtifactPath(manifest.ID, "manifest.json")
	setIfExists := func(target *string, elements ...string) error {
		path, err := service.store.ArtifactPath(manifest.ID, elements...)
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			*target = path
			return nil
		} else if errors.Is(err, os.ErrNotExist) {
			return nil
		} else {
			return err
		}
	}
	for _, item := range []struct {
		target   *string
		elements []string
	}{
		{&result.Artifacts.Snapshot, []string{"git", "snapshot.json"}},
		{&result.Artifacts.Capabilities, []string{"context", "capabilities.json"}},
		{&result.Artifacts.ModelPolicy, []string{"context", "model-policy.txt"}},
		{&result.Artifacts.Packet, []string{"packet", "review-packet.json"}},
		{&result.Artifacts.RoutingRequest, []string{"routing-request.json"}},
		{&result.Artifacts.Routing, []string{"routing.json"}},
		{&result.Artifacts.Candidates, []string{"evidence", "prechecked.json"}},
		{&result.Artifacts.MinimalEvidence, []string{"evidence", "minimal.json"}},
		{&result.Artifacts.Verdicts, []string{"evidence", "verdicts.json"}},
		{&result.Artifacts.ReviewJSON, []string{"review.json"}},
		{&result.Artifacts.ReviewMarkdown, []string{"review.md"}},
		{&result.Artifacts.Rejected, []string{"rejected-findings.json"}},
		{&result.Artifacts.Trace, []string{"trace.json"}},
	} {
		if err := setIfExists(item.target, item.elements...); err != nil {
			return InspectResult{}, err
		}
	}
	if result.Artifacts.Routing != "" {
		routeResult, err := decodeStrict[routing.Result](result.Artifacts.Routing)
		if err != nil {
			return InspectResult{}, err
		}
		result.Routing = &routeResult
		result.Counts.SelectedRoles = len(routeResult.Selected)
		for _, decision := range routeResult.Selected {
			path, _ := service.store.ArtifactPath(manifest.ID, "evidence", "precheck", decision.Role+".json")
			if _, err := os.Stat(path); err == nil {
				result.Counts.ValidatedRoles++
			}
		}
	}
	if result.Artifacts.Capabilities != "" {
		capabilities, err := loadCapabilities(service, manifest)
		if err != nil {
			return InspectResult{}, err
		}
		result.Capabilities = append([]CapabilityRecord(nil), capabilities.Capabilities...)
		for _, capability := range capabilities.Capabilities {
			if capability.Status == CapabilityUnavailable {
				appendUnique(&result.CoverageLimits, "mcp:"+string(capability.Source)+": "+capability.Reason)
			}
		}
	}
	coverageDoc, coverageErr := loadCoverage(service, manifest)
	if coverageErr == nil {
		for _, limit := range coverageDoc.Limits {
			value := limit.Source + ": " + limit.Reason
			appendUnique(&result.CoverageLimits, value)
			if strings.HasPrefix(limit.Source, "reviewer:") {
				result.Counts.FailedRoles++
			}
		}
	} else if !errors.Is(unwrapPathError(coverageErr), os.ErrNotExist) {
		return InspectResult{}, coverageErr
	}
	if result.Artifacts.ReviewJSON != "" {
		reviewValue, err := decodeStrict[report.Review](result.Artifacts.ReviewJSON)
		if err != nil {
			return InspectResult{}, err
		}
		result.Review = &reviewValue
		result.Counts.ConfirmedFindings = len(reviewValue.Findings)
		result.Counts.NeedsHuman = len(reviewValue.NeedsHuman)
		for _, finding := range reviewValue.Findings {
			result.Counts.BySeverity[string(finding.Candidate.Severity)]++
		}
	}
	stalePath, _ := service.store.ArtifactPath(manifest.ID, "git", "staleness.json")
	if stale, err := decodeStrict[gitcontext.Staleness](stalePath); err == nil {
		result.Staleness = &stale
	} else if !errors.Is(unwrapPathError(err), os.ErrNotExist) {
		return InspectResult{}, err
	}
	sort.Strings(result.CoverageLimits)
	return result, nil
}
