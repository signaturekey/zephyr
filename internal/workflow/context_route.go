package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/gitcontext"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/signaturekey/zephyr/internal/trace"
)

func (service *Service) AddContext(ctx context.Context, options ContextAddOptions) (ContextAddResult, error) {
	if err := requireService(service); err != nil {
		return ContextAddResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ContextAddResult{}, err
	}
	unlock, err := service.lockRun(ctx, options.RunID)
	if err != nil {
		return ContextAddResult{}, err
	}
	defer unlock()
	if int64(len(options.Content)) > maxContextBytes {
		return ContextAddResult{}, fmt.Errorf("business context exceeds %d bytes", maxContextBytes)
	}
	manifest, err := service.store.Load(ctx, options.RunID)
	if err != nil {
		return ContextAddResult{}, err
	}
	if err := ensureStage(manifest, "collect", run.StageComplete); err != nil {
		return ContextAddResult{}, fmt.Errorf("business context requires a completed Git collection: %w", err)
	}
	if err := ensureStage(manifest, "route", run.StagePending); err != nil {
		return ContextAddResult{}, fmt.Errorf("business context must be frozen before routing: %w", err)
	}
	if err := ensureStage(manifest, "review", run.StagePending); err != nil {
		return ContextAddResult{}, fmt.Errorf("business context cannot change after reviewers start: %w", err)
	}
	options.Source = strings.ToLower(strings.TrimSpace(options.Source))
	capabilities, err := loadCapabilities(service, manifest)
	if err != nil {
		return ContextAddResult{}, err
	}
	if err := requireAvailableCapability(capabilities, CapabilitySource(options.Source)); err != nil {
		return ContextAddResult{}, err
	}
	cfg, err := loadEffectiveConfig(service, manifest)
	if err != nil {
		return ContextAddResult{}, err
	}
	event, err := service.startTrace(manifest, "context", map[string]string{
		"source": strings.ToLower(strings.TrimSpace(options.Source)),
		"key":    strings.TrimSpace(options.Key),
	})
	if err != nil {
		return ContextAddResult{}, err
	}
	path, snapshot, operationErr := contextpack.SaveBusinessSnapshot(manifest.RunDir, contextpack.BusinessSnapshotInput{
		Source:    options.Source,
		Key:       options.Key,
		URL:       options.URL,
		FetchedAt: service.now(),
		Content:   string(options.Content),
	}, redactionPolicy(cfg))
	status := trace.StatusCompleted
	if operationErr != nil {
		status = trace.StatusFailed
	}
	if traceErr := event.finish(status, operationErr); operationErr == nil && traceErr != nil {
		operationErr = traceErr
	}
	if operationErr != nil {
		return ContextAddResult{}, operationErr
	}
	return ContextAddResult{
		RunID:       manifest.ID,
		Path:        path,
		Source:      snapshot.Source,
		Key:         snapshot.Key,
		FetchedAt:   snapshot.FetchedAt,
		ContentHash: snapshot.ContentHash,
	}, nil
}

func (service *Service) AddCoverage(ctx context.Context, options CoverageAddOptions) (CoverageDocument, error) {
	if err := requireService(service); err != nil {
		return CoverageDocument{}, err
	}
	unlock, err := service.lockRun(ctx, options.RunID)
	if err != nil {
		return CoverageDocument{}, err
	}
	defer unlock()
	return service.addCoverage(ctx, options)
}

func (service *Service) addCoverage(ctx context.Context, options CoverageAddOptions) (CoverageDocument, error) {
	options.Source = strings.TrimSpace(options.Source)
	options.Reason = strings.TrimSpace(options.Reason)
	if options.Source == "" || options.Reason == "" {
		return CoverageDocument{}, errors.New("coverage source and reason are required")
	}
	if !options.AllowAfterRoute && (options.Source == "evidence-gate" || strings.HasPrefix(options.Source, "reviewer:")) {
		return CoverageDocument{}, fmt.Errorf("coverage source namespace %q is reserved for typed workflow failures", options.Source)
	}
	manifest, err := service.store.Load(ctx, options.RunID)
	if err != nil {
		return CoverageDocument{}, err
	}
	if !options.AllowAfterRoute {
		if err := ensureStage(manifest, "collect", run.StageComplete); err != nil {
			return CoverageDocument{}, fmt.Errorf("context coverage requires a completed Git collection: %w", err)
		}
		if err := ensureStage(manifest, "route", run.StagePending); err != nil {
			return CoverageDocument{}, fmt.Errorf("context coverage must be frozen before routing: %w", err)
		}
		if err := ensureStage(manifest, "review", run.StagePending); err != nil {
			return CoverageDocument{}, fmt.Errorf("context coverage cannot change after reviewers start: %w", err)
		}
	}
	doc, err := loadCoverage(service, manifest)
	if err != nil {
		return CoverageDocument{}, err
	}
	cfg, err := loadEffectiveConfig(service, manifest)
	if err != nil {
		return CoverageDocument{}, err
	}
	policy := redactionPolicy(cfg)
	options.Source = policy.Text(options.Source)
	options.Reason = policy.Text(options.Reason)
	appendCoverage(&doc, options.Source, options.Reason)
	sort.Slice(doc.Limits, func(i, j int) bool {
		if doc.Limits[i].Source == doc.Limits[j].Source {
			return doc.Limits[i].Reason < doc.Limits[j].Reason
		}
		return doc.Limits[i].Source < doc.Limits[j].Source
	})
	if _, err := service.store.WriteJSON(ctx, manifest.ID, doc, "context", "coverage-limits.json"); err != nil {
		return CoverageDocument{}, err
	}
	appendUnique(&manifest.CoverageLimits, options.Source+": "+options.Reason)
	if err := service.store.Save(ctx, manifest); err != nil {
		return CoverageDocument{}, err
	}
	return doc, nil
}

func (service *Service) Route(ctx context.Context, options RouteOptions) (result RouteResult, returnErr error) {
	if err := requireService(service); err != nil {
		return result, err
	}
	unlock, err := service.lockRun(ctx, options.RunID)
	if err != nil {
		return result, err
	}
	defer unlock()
	manifest, err := service.store.Load(ctx, options.RunID)
	if err != nil {
		return result, err
	}
	if err := ensureStage(manifest, "collect", run.StageComplete); err != nil {
		return result, fmt.Errorf("route review: %w", err)
	}
	if err := ensureStage(manifest, "route", run.StagePending); err != nil {
		return result, fmt.Errorf("route review is terminal; start a new run: %w", err)
	}
	if err := ensureStage(manifest, "review", run.StagePending); err != nil {
		return result, fmt.Errorf("route review: immutable packet is already in use: %w", err)
	}
	capabilities, err := loadCapabilities(service, manifest)
	if err != nil {
		return result, fmt.Errorf("load capability preflight: %w", err)
	}
	if err := validateCapabilityPreflight(capabilities); err != nil {
		return result, err
	}
	if err := manifest.SetStage("route", run.StageRunning, service.now(), ""); err != nil {
		return result, err
	}
	manifest.State = run.StateRunning
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	event, err := service.startTrace(manifest, "route", nil)
	if err != nil {
		return result, err
	}
	defer func() {
		status := trace.StatusCompleted
		if returnErr != nil {
			status = trace.StatusFailed
			_ = manifest.SetStage("route", run.StageFailed, service.now(), safeError(returnErr))
			manifest.State = run.StateFailed
			_ = service.store.Save(context.Background(), manifest)
		}
		if err := event.finish(status, returnErr); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	snapshot, err := artifact[gitcontext.Snapshot](service, manifest, "git", "snapshot.json")
	if err != nil {
		return result, err
	}
	staleness, err := service.collector.CheckStale(ctx, snapshot)
	if err != nil {
		return result, err
	}
	if staleness.Stale {
		return result, errors.New("Git input changed after collection; start a new run instead of mixing snapshots")
	}
	cfg, err := artifact[config.Config](service, manifest, "context", "config.json")
	if err != nil {
		return result, err
	}
	coverage, err := loadCoverage(service, manifest)
	if err != nil {
		return result, err
	}
	for _, capability := range capabilities.Capabilities {
		if capability.Status == CapabilityUnavailable {
			appendCoverage(&coverage, "mcp:"+string(capability.Source), capability.Reason)
		}
	}
	changedFiles, gitExclusions := packetPathsAndExclusions(snapshot)
	instructions, err := artifact[contextpack.InstructionSnapshot](service, manifest, "context", "project-instructions", "index.json")
	if err != nil {
		return result, err
	}
	planPath, err := service.store.ArtifactPath(manifest.ID, "context", "review-spec.md")
	if err != nil {
		return result, err
	}
	if _, err := os.Stat(planPath); errors.Is(err, os.ErrNotExist) {
		planPath = ""
	} else if err != nil {
		return result, fmt.Errorf("inspect immutable plan: %w", err)
	}
	packetResult, err := contextpack.Build(contextpack.Options{
		RunDir:   manifest.RunDir,
		RunID:    manifest.ID,
		Mode:     string(manifest.Mode),
		Source:   string(manifest.Source),
		RepoRoot: snapshot.Repository.Root,
		Repository: contextpack.Repository{
			Root:        snapshot.Repository.Root,
			Branch:      snapshot.Repository.Branch,
			Head:        snapshot.Repository.HeadSHA,
			BaseRef:     snapshot.Repository.BaseRef,
			BaseSHA:     snapshot.Repository.BaseSHA,
			TargetSHA:   snapshot.Repository.TargetSHA,
			MergeBase:   snapshot.Repository.MergeBaseSHA,
			CommitRange: snapshot.Repository.CommitRange,
		},
		ChangedFiles:       changedFiles,
		PlanPath:           planPath,
		Redaction:          redactionPolicy(cfg),
		Instructions:       instructions,
		ExcludedSources:    gitExclusions,
		UnavailableSources: coverage.Limits,
	})
	if err != nil {
		return result, err
	}
	packetBytes, err := json.Marshal(packetResult.Packet)
	if err != nil {
		return result, fmt.Errorf("encode review packet for validation: %w", err)
	}
	if err := schema.ValidateReviewInputBytes(packetBytes); err != nil {
		return result, err
	}
	packetArtifactBytes, err := json.MarshalIndent(packetResult.Packet, "", "  ")
	if err != nil {
		return result, fmt.Errorf("encode immutable packet identity: %w", err)
	}
	packetArtifactBytes = append(packetArtifactBytes, '\n')
	packetDigest := fmt.Sprintf("%x", sha256.Sum256(packetArtifactBytes))
	routingRequest, err := routing.PrepareSemantic(cfg, routing.Input{
		Mode:          routing.Mode(manifest.Mode),
		ChangedPaths:  packetResult.Packet.ChangedFiles,
		Signals:       packetResult.Packet.RoutingSignals,
		StrongSignals: packetResult.Packet.StrongRoutingSignals,
		HasPlan:       packetResult.Packet.Plan != nil,
		HasChanges:    len(changedFiles) > 0,
		ForceInclude:  options.ForceInclude,
		ForceExclude:  options.ForceExclude,
	}, manifest.ID, packetDigest, semanticEvidenceSources(packetResult.Packet))
	if err != nil {
		return result, err
	}
	staleness, err = service.collector.CheckStale(ctx, snapshot)
	if err != nil {
		return result, err
	}
	if staleness.Stale {
		return result, errors.New("Git input changed while routing; start a new run")
	}
	packetPath, err := service.store.WriteJSON(ctx, manifest.ID, packetResult.Packet, "packet", "review-packet.json")
	if err != nil {
		return result, err
	}
	if _, err := service.store.WriteJSON(ctx, manifest.ID, packetResult.Truncations, "packet", "truncation.json"); err != nil {
		return result, err
	}
	if _, err := service.store.WriteJSON(ctx, manifest.ID, packetResult.Packet.Sources, "context", "sources.json"); err != nil {
		return result, err
	}
	routingRequestPath, err := service.store.WriteJSON(ctx, manifest.ID, routingRequest, "routing-request.json")
	if err != nil {
		return result, err
	}
	manifest.Mode = run.Mode(routingRequest.Mode)
	manifest.State = run.StateRunning
	routingPath := ""
	var routeResult routing.Result
	if len(routingRequest.Candidates) == 0 {
		routeResult, err = routing.ResolveDeterministic(routingRequest)
		if err != nil {
			return result, err
		}
		routingPath, err = service.store.WriteJSON(ctx, manifest.ID, routeResult, "routing.json")
		if err != nil {
			return result, err
		}
		if err := manifest.SetStage("route", run.StageComplete, service.now(), ""); err != nil {
			return result, err
		}
	}
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	if len(routingRequest.Candidates) > 0 {
		if _, err := service.startTrace(manifest, "semantic-routing", map[string]string{
			"protected_roles":     fmt.Sprintf("%d", len(routingRequest.Protected)),
			"semantic_candidates": fmt.Sprintf("%d", len(routingRequest.Candidates)),
		}); err != nil {
			return result, err
		}
	}
	return RouteResult{
		RunID:              manifest.ID,
		PacketPath:         packetPath,
		RoutingRequestPath: routingRequestPath,
		RoutingPath:        routingPath,
		RoutingRequest:     routingRequest,
		Routing:            routeResult,
	}, nil
}

func semanticEvidenceSources(packet contextpack.Packet) []routing.EvidenceSource {
	sources := []routing.EvidenceSource{{ID: "scope", Kind: "scope", Source: "packet scope and limitations"}}
	if len(packet.ChangedFiles) > 0 {
		sources = append(sources, routing.EvidenceSource{ID: "changed-paths", Kind: "changed-paths", Source: "changed_files"})
	}
	if packet.Diff.Full != "" {
		sources = append(sources, routing.EvidenceSource{ID: "diff", Kind: "diff", Source: "diff.full"})
	}
	if packet.Plan != nil {
		sources = append(sources, routing.EvidenceSource{ID: "plan", Kind: "plan", Source: packet.Plan.Path, ContentHash: packet.Plan.ContentHash})
	}
	for index, business := range packet.BusinessContext {
		sources = append(sources, routing.EvidenceSource{
			ID: fmt.Sprintf("business-%03d", index+1), Kind: "business-context",
			Source: business.Source + ":" + business.Key, ContentHash: business.ContentHash,
		})
	}
	return sources
}
