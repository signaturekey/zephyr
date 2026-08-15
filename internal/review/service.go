package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/signaturekey/zephyr/internal/agent"
	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/report"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/schema"
	"github.com/signaturekey/zephyr/internal/snapshot"
)

var ErrInvalidRequest = errors.New("invalid review request")

type Request struct {
	Repository  string
	Source      snapshot.Source
	Commit      string
	Branch      string
	Base        string
	ConfigPath  string
	Contexts    []string
	IncludeRole []string
	ExcludeRole []string
	MaxParallel int
	KeepTemp    bool
}

type Result struct {
	Review       report.Review
	Markdown     []byte
	JSON         []byte
	SnapshotRoot string
}

type RuntimeFactory func(context.Context, config.Config) (agent.Runtime, error)

type Service struct {
	RuntimeFactory RuntimeFactory
	Now            func() time.Time
}

func (service Service) Run(ctx context.Context, request Request) (Result, error) {
	if service.RuntimeFactory == nil {
		return Result{}, errors.New("review runtime factory is required")
	}
	if service.Now == nil {
		service.Now = time.Now
	}
	if request.Repository == "" {
		request.Repository = "."
	}
	if request.Source == "" {
		request.Source = snapshot.SourceWorktree
	}
	snap, err := snapshot.Acquire(ctx, snapshot.Request{
		Repository: request.Repository, Source: request.Source, Commit: request.Commit,
		Branch: request.Branch, Base: request.Base, KeepTemp: request.KeepTemp,
	})
	if err != nil {
		return Result{}, err
	}
	defer snap.Cleanup()

	cfg, err := loadConfig(request.ConfigPath, snap.Root)
	if err != nil {
		return Result{}, err
	}
	maxParallel := request.MaxParallel
	if maxParallel == 0 {
		maxParallel = cfg.Limits.MaxParallelReviewers
	}
	if maxParallel < 1 {
		return Result{}, fmt.Errorf("%w: max parallel must be positive", ErrInvalidRequest)
	}
	contexts, err := readContexts(request.Contexts)
	if err != nil {
		return Result{}, err
	}
	runID := service.Now().UTC().Format("20060102T150405.000000000Z")
	signals, strong := routing.DetectSignals(snap.ChangedPaths, snap.Diff)
	evidenceSources := []routing.EvidenceSource{
		{ID: "snapshot.diff", Kind: "diff", Source: "frozen snapshot diff"},
		{ID: "snapshot.paths", Kind: "paths", Source: "frozen changed path index"},
	}
	for index, document := range contexts {
		evidenceSources = append(evidenceSources, routing.EvidenceSource{ID: fmt.Sprintf("context.%d", index+1), Kind: "context", Source: document.Name})
	}
	routingRequest, err := routing.Prepare(cfg, routing.Input{
		RunID: runID, ChangedPaths: snap.ChangedPaths, Signals: signals, StrongSignals: strong,
		Include: request.IncludeRole, Exclude: request.ExcludeRole, Evidence: evidenceSources,
	})
	if err != nil {
		return Result{}, err
	}

	runtime, err := service.RuntimeFactory(ctx, cfg)
	if err != nil {
		return Result{}, fmt.Errorf("start Aether runtime: %w", err)
	}
	defer runtime.Close()

	var routingResult routing.Result
	var coverage []string
	if len(routingRequest.Candidates) == 0 {
		routingResult = routing.Deterministic(routingRequest)
	} else {
		proposal, routeErr := runtime.Route(ctx, routingRequest, snap, contexts)
		if routeErr != nil {
			routingResult = routing.Fallback(routingRequest, routeErr.Error())
			coverage = append(coverage, "semantic routing failed; every unresolved role was included conservatively: "+routeErr.Error())
		} else if routingResult, err = routing.Resolve(routingRequest, proposal); err != nil {
			routingResult = routing.Fallback(routingRequest, err.Error())
			coverage = append(coverage, "semantic routing output was invalid; every unresolved role was included conservatively: "+err.Error())
		}
	}

	type roleResult struct {
		envelope schema.CandidateEnvelope
		err      error
	}
	roleResults := make([]roleResult, len(routingResult.Selected))
	roleExecutions := make([]report.RoleExecution, len(routingResult.Selected))
	semaphore := make(chan struct{}, maxParallel)
	var wait sync.WaitGroup
	for index, decision := range routingResult.Selected {
		index, role := index, decision.Role
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				roleResults[index].err = ctx.Err()
				return
			}
			roleResults[index].envelope, roleResults[index].err = runtime.Review(ctx, runID, role, snap, contexts)
		}()
	}
	wait.Wait()

	prechecks := make([]evidence.PrecheckReport, 0, len(roleResults))
	for index, result := range roleResults {
		role := routingResult.Selected[index].Role
		if result.err != nil {
			message := conciseError(result.err)
			roleExecutions[index] = report.RoleExecution{Role: role, Status: "failed", Error: message}
			coverage = append(coverage, fmt.Sprintf("role %s failed: %s", role, message))
			continue
		}
		roleExecutions[index] = report.RoleExecution{Role: role, Status: "complete"}
		prechecks = append(prechecks, evidence.Precheck(result.envelope, evidence.Scope{
			RunID: runID, Diff: snap.Diff, ChangedFiles: snap.ChangedPaths, Config: cfg,
		}))
	}
	candidates := evidence.MergeCandidateReports(runID, prechecks)
	verdicts := schema.EvidenceVerdictEnvelope{Version: schema.ProtocolVersion, RunID: runID, Verdicts: []schema.EvidenceVerdict{}}
	evidenceStatus := "skipped-no-candidates"
	if len(candidates.Findings) > 0 {
		verdicts, err = runtime.Gate(ctx, runID, candidates.Findings, snap, contexts)
		if err != nil {
			return Result{}, fmt.Errorf("evidence gate failed: %w", err)
		}
		if err := evidence.ValidateVerdicts(verdicts, candidates); err != nil {
			return Result{}, fmt.Errorf("evidence gate returned invalid verdicts: %w", err)
		}
		evidenceStatus = "validated"
	}

	contextNames := make([]string, 0, len(contexts))
	for _, document := range contexts {
		contextNames = append(contextNames, document.Name)
	}
	finalReport, err := report.Aggregate(report.AggregateInput{
		RunID: runID, GeneratedAt: service.Now(),
		Scope: report.Scope{
			Source: snap.Source, Repository: snap.Repository, Branch: snap.Branch,
			HeadSHA: snap.HeadSHA, BaseRef: snap.BaseRef, BaseSHA: snap.BaseSHA,
			MergeBase: snap.MergeBase, ChangedFiles: snap.ChangedPaths, Contexts: contextNames,
		},
		Routing: routingResult, MaxParallel: maxParallel, Roles: roleExecutions,
		Candidates: candidates, Verdicts: verdicts, PrecheckReports: prechecks,
		CoverageLimits: coverage, EvidenceStatus: evidenceStatus,
	})
	if err != nil {
		return Result{}, err
	}
	markdown, err := report.RenderMarkdown(finalReport)
	if err != nil {
		return Result{}, err
	}
	jsonReport, err := json.MarshalIndent(finalReport, "", "  ")
	if err != nil {
		return Result{}, fmt.Errorf("encode JSON report: %w", err)
	}
	jsonReport = append(jsonReport, '\n')
	return Result{Review: finalReport, Markdown: markdown, JSON: jsonReport, SnapshotRoot: snap.Root}, nil
}

func loadConfig(explicit, snapshotRoot string) (config.Config, error) {
	path := explicit
	if path == "" {
		candidate := filepath.Join(snapshotRoot, ".zephyr", "config.yaml")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
		} else if !errors.Is(err, os.ErrNotExist) {
			return config.Config{}, fmt.Errorf("inspect project config: %w", err)
		}
	}
	return config.Load(path)
}

func readContexts(paths []string) ([]agent.ContextDocument, error) {
	documents := make([]agent.ContextDocument, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read context %q: %w", path, err)
		}
		documents = append(documents, agent.ContextDocument{Name: filepath.Base(path), Content: string(data)})
	}
	sort.SliceStable(documents, func(i, j int) bool { return documents[i].Name < documents[j].Name })
	return documents, nil
}

func conciseError(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 500 {
		message = message[:500] + "…"
	}
	return message
}
