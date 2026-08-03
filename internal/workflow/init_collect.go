package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/gitcontext"
	"github.com/signaturekey/zephyr/internal/redaction"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/trace"
)

func (service *Service) Init(ctx context.Context, options InitOptions) (result InitResult, returnErr error) {
	if err := requireService(service); err != nil {
		return result, err
	}
	if options.Repository == "" {
		options.Repository = "."
	}
	if options.Mode == "" {
		options.Mode = run.ModeAuto
	}
	if err := options.Mode.Validate(); err != nil {
		return result, fmt.Errorf("initialize run: %w", err)
	}
	if options.BaseRef != "" && options.CommitRange != "" {
		return result, errors.New("initialize run: --base and --range are mutually exclusive")
	}
	if options.Source == "" {
		switch {
		case options.CommitRange != "":
			options.Source = run.SourceCommitRange
		case options.BaseRef != "":
			options.Source = run.SourceBranch
		}
	}
	if options.Source != "" {
		if err := options.Source.Validate(); err != nil {
			return result, fmt.Errorf("initialize run: %w", err)
		}
	}

	var plan []byte
	if strings.TrimSpace(options.PlanPath) != "" {
		absolute, err := filepath.Abs(options.PlanPath)
		if err != nil {
			return result, fmt.Errorf("resolve plan %q: %w", options.PlanPath, err)
		}
		options.PlanPath = filepath.Clean(absolute)
		plan, err = readPlan(options.Repository, options.PlanPath, maxPlanBytes)
		if err != nil {
			return result, fmt.Errorf("initialize run: %w", err)
		}
		if len(strings.TrimSpace(string(plan))) == 0 {
			return result, errors.New("initialize run: plan is empty")
		}
		if !utf8.Valid(plan) || strings.IndexByte(string(plan), 0) >= 0 {
			return result, errors.New("initialize run: plan must be UTF-8 text without NUL bytes")
		}
	}
	if (options.Mode == run.ModePlan || options.Mode == run.ModeAlignment) && len(plan) == 0 {
		return result, fmt.Errorf("initialize run: mode %q requires --plan", options.Mode)
	}
	if options.Source == run.SourcePlanOnly && len(plan) == 0 {
		return result, errors.New("initialize run: plan-only source requires --plan")
	}
	if options.Mode == run.ModeImplementation && options.Source == run.SourcePlanOnly {
		return result, errors.New("initialize run: implementation mode cannot use plan-only source")
	}

	manifest, err := service.store.Create(ctx, run.CreateOptions{
		Mode:        options.Mode,
		Source:      options.Source,
		Repository:  options.Repository,
		BaseRef:     options.BaseRef,
		CommitRange: options.CommitRange,
		PlanPath:    options.PlanPath,
	})
	if err != nil {
		return result, err
	}
	event, err := service.startTrace(manifest, "init", map[string]string{
		"mode": string(manifest.Mode), "source": string(manifest.Source),
	})
	if err != nil {
		return result, fmt.Errorf("start init trace: %w", err)
	}
	defer func() {
		status := trace.StatusCompleted
		if returnErr != nil {
			status = trace.StatusFailed
			_ = manifest.SetStage("init", run.StageFailed, service.now(), safeError(returnErr))
			manifest.State = run.StateFailed
			_ = service.store.Save(context.Background(), manifest)
		}
		if err := event.finish(status, returnErr); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	if len(plan) != 0 {
		plan = []byte(redaction.DefaultPolicy(nil).Text(string(plan)))
		if _, err := service.store.WriteArtifact(ctx, manifest.ID, plan, "context", "review-spec.md"); err != nil {
			return result, fmt.Errorf("snapshot plan: %w", err)
		}
	}
	if _, err := service.store.WriteJSON(ctx, manifest.ID, CoverageDocument{
		Version: 1,
		RunID:   manifest.ID,
		Limits:  []contextpack.CoverageLimit{},
	}, "context", "coverage-limits.json"); err != nil {
		return result, fmt.Errorf("initialize coverage artifact: %w", err)
	}
	if _, err := service.store.WriteJSON(ctx, manifest.ID, CapabilityDocument{
		Version:      capabilityDocumentVersion,
		RunID:        manifest.ID,
		Capabilities: []CapabilityRecord{},
	}, "context", "capabilities.json"); err != nil {
		return result, fmt.Errorf("initialize capability artifact: %w", err)
	}
	manifestPath, err := service.store.ArtifactPath(manifest.ID, "manifest.json")
	if err != nil {
		return result, err
	}
	return InitResult{RunID: manifest.ID, RunDir: manifest.RunDir, Manifest: manifestPath}, nil
}

func (service *Service) Collect(ctx context.Context, options CollectOptions) (result CollectResult, returnErr error) {
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
	if err := ensureStage(manifest, "route", run.StagePending, run.StageFailed); err != nil {
		return result, fmt.Errorf("collect immutable snapshot: %w", err)
	}
	if err := ensureStage(manifest, "collect", run.StagePending); err != nil {
		return result, fmt.Errorf("collect immutable snapshot is terminal; start a new run: %w", err)
	}
	if err := ensureStage(manifest, "review", run.StagePending); err != nil {
		return result, fmt.Errorf("collect immutable snapshot: reviewers already started: %w", err)
	}
	cfg, err := loadConfig(manifest.Repository)
	if err != nil {
		return result, fmt.Errorf("collect immutable snapshot: %w", err)
	}
	if err := manifest.SetStage("collect", run.StageRunning, service.now(), ""); err != nil {
		return result, err
	}
	manifest.State = run.StateRunning
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	event, err := service.startTrace(manifest, "collect", map[string]string{"source": string(manifest.Source)})
	if err != nil {
		return result, err
	}
	defer func() {
		status := trace.StatusCompleted
		if returnErr != nil {
			status = trace.StatusFailed
			_ = manifest.SetStage("collect", run.StageFailed, service.now(), safeError(returnErr))
			manifest.State = run.StateFailed
			_ = service.store.Save(context.Background(), manifest)
		}
		if err := event.finish(status, returnErr); returnErr == nil && err != nil {
			returnErr = err
		}
	}()
	if _, err := service.store.WriteJSON(ctx, manifest.ID, cfg, "context", "config.json"); err != nil {
		return result, err
	}
	snapshot, err := service.collector.Collect(ctx, gitcontext.Options{
		Repository:              manifest.Repository,
		Source:                  manifest.Source,
		BaseRef:                 manifest.BaseRef,
		CommitRange:             manifest.CommitRange,
		RestrictedPatterns:      collectorRestrictedPatterns(cfg),
		IncludeGenerated:        options.IncludeGenerated,
		IncludeVendor:           options.IncludeVendor,
		IncludeUntrackedContent: options.IncludeUntrackedContent,
		MaxUntrackedBytes:       options.MaxUntrackedBytes,
	})
	if err != nil {
		return result, err
	}
	changedFiles, _ := packetPathsAndExclusions(snapshot)
	instructionSnapshot, err := service.freezeProjectInstructions(ctx, snapshot, changedFiles, cfg)
	if err != nil {
		return result, fmt.Errorf("freeze project instructions: %w", err)
	}
	staleness, err := service.collector.CheckStale(ctx, snapshot)
	if err != nil {
		return result, fmt.Errorf("verify repository after freezing project instructions: %w", err)
	}
	if staleness.Stale {
		return result, errors.New("repository changed while freezing immutable context; start a new run")
	}
	if _, err := service.store.WriteJSON(ctx, manifest.ID, instructionSnapshot, "context", "project-instructions", "index.json"); err != nil {
		return result, fmt.Errorf("persist project instruction snapshot: %w", err)
	}
	snapshotPath, err := service.store.WriteJSON(ctx, manifest.ID, snapshot, "git", "snapshot.json")
	if err != nil {
		return result, err
	}
	metadataPath, err := service.store.WriteJSON(ctx, manifest.ID, snapshot.MetadataArtifact(), "git", "metadata.json")
	if err != nil {
		return result, err
	}
	statusPath, err := service.store.WriteJSON(ctx, manifest.ID, snapshot.StatusArtifact(), "git", "status.json")
	if err != nil {
		return result, err
	}
	for path, data := range map[string]string{
		"diff.patch":     snapshot.Patches.Full,
		"staged.patch":   snapshot.Patches.Staged,
		"unstaged.patch": snapshot.Patches.Unstaged,
	} {
		if _, err := service.store.WriteArtifact(ctx, manifest.ID, []byte(data), "git", path); err != nil {
			return result, err
		}
	}

	planPath, err := service.store.ArtifactPath(manifest.ID, "context", "review-spec.md")
	if err != nil {
		return result, err
	}
	hasPlan := false
	if _, err := os.Stat(planPath); err == nil {
		hasPlan = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("inspect plan snapshot: %w", err)
	}
	hasChanges := len(changedFiles) > 0
	resolved, err := run.ResolveMode(manifest.Mode, hasPlan, hasChanges)
	if err != nil {
		return result, err
	}
	switch resolved {
	case run.ModePlan:
		if !hasPlan {
			return result, errors.New("plan mode requires a plan snapshot")
		}
	case run.ModeImplementation:
		if !hasChanges {
			return result, errors.New("implementation mode requires a safe Git change; restricted or unconsented untracked names alone do not count")
		}
	case run.ModeAlignment:
		if !hasPlan || !hasChanges {
			return result, errors.New("alignment mode requires both a plan snapshot and a reviewable Git diff")
		}
	}
	manifest.Mode = resolved
	manifest.GitSnapshot = &run.GitSnapshotRef{
		HeadSHA:                snapshot.Repository.HeadSHA,
		BaseSHA:                snapshot.Repository.BaseSHA,
		TargetSHA:              snapshot.Repository.TargetSHA,
		MergeBaseSHA:           snapshot.Repository.MergeBaseSHA,
		SourceFingerprint:      snapshot.SourceFingerprint,
		WorkingTreeFingerprint: snapshot.WorkingTreeFingerprint,
		CollectedAt:            snapshot.CollectedAt,
	}
	for _, limitation := range snapshot.Limitations {
		appendUnique(&manifest.CoverageLimits, limitation)
	}
	if err := manifest.SetStage("collect", run.StageComplete, service.now(), ""); err != nil {
		return result, err
	}
	manifest.State = run.StateRunning
	if err := service.store.Save(ctx, manifest); err != nil {
		return result, err
	}
	return CollectResult{
		RunID:        manifest.ID,
		Mode:         manifest.Mode,
		Source:       manifest.Source,
		Reviewable:   hasChanges,
		Stats:        snapshot.Stats,
		SnapshotPath: snapshotPath,
		MetadataPath: metadataPath,
		StatusPath:   statusPath,
	}, nil
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return redaction.DefaultPolicy(nil).Text(err.Error())
}

func appendUnique(values *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
}
