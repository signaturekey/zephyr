package workflow

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/gitcontext"
	"github.com/signaturekey/zephyr/internal/safefile"
)

const maxInstructionBytes int64 = 512 << 10

func packetPathsAndExclusions(snapshot gitcontext.Snapshot) ([]string, []contextpack.CoverageLimit) {
	set := make(map[string]struct{})
	limits := make([]contextpack.CoverageLimit, 0)
	restricted := 0
	untrackedExcluded := 0
	for _, change := range snapshot.Changes {
		if change.Restricted {
			restricted++
			continue
		}
		path := filepathSlash(change.Path)
		set[path] = struct{}{}
		if !change.ContentIncluded {
			reason := strings.TrimSpace(change.ExclusionReason)
			if reason == "" {
				reason = "content policy"
			}
			limits = append(limits, contextpack.CoverageLimit{Source: path, Reason: "Git content excluded: " + reason})
		}
	}
	for _, file := range snapshot.Status.Untracked {
		switch {
		case file.Restricted:
			restricted++
		case file.ContentIncluded:
			set[filepathSlash(file.Path)] = struct{}{}
		case file.Path != "":
			untrackedExcluded++
		}
	}
	if restricted > 0 {
		limits = append(limits, contextpack.CoverageLimit{
			Source: "git:restricted", Reason: fmt.Sprintf("%d changed path(s) excluded without exposing names", restricted),
		})
	}
	if untrackedExcluded > 0 {
		limits = append(limits, contextpack.CoverageLimit{
			Source: "git:untracked", Reason: fmt.Sprintf("%d untracked file name(s) observed; content excluded without explicit consent", untrackedExcluded),
		})
	}
	paths := make([]string, 0, len(set))
	for value := range set {
		paths = append(paths, value)
	}
	sort.Strings(paths)
	sort.Slice(limits, func(i, j int) bool { return limits[i].Source < limits[j].Source })
	return paths, limits
}

func (service *Service) freezeProjectInstructions(
	ctx context.Context,
	snapshot gitcontext.Snapshot,
	changedFiles []string,
	cfg config.Config,
) (contextpack.InstructionSnapshot, error) {
	candidates := contextpack.InstructionCandidates(changedFiles)
	tracked, err := service.collector.TrackedPaths(ctx, snapshot.Repository.Root, candidates)
	if err != nil {
		return contextpack.InstructionSnapshot{}, err
	}
	explicitUntracked := make(map[string]bool)
	for _, file := range snapshot.Status.Untracked {
		if file.ContentIncluded && !file.Restricted {
			explicitUntracked[filepathSlash(file.Path)] = true
		}
	}
	policy := redactionPolicy(cfg)
	allowed := make([]string, 0, len(candidates))
	excluded := make([]contextpack.CoverageLimit, 0)
	for _, candidate := range candidates {
		if policy.PathDenied(candidate) {
			excluded = append(excluded, contextpack.CoverageLimit{Source: "project-instruction", Reason: "candidate denied by repository policy"})
			continue
		}
		exists, inspectErr := safefile.ExistsBeneath(snapshot.Repository.Root, candidate)
		if inspectErr != nil {
			excluded = append(excluded, contextpack.CoverageLimit{Source: candidate, Reason: safeError(inspectErr)})
			continue
		}
		if !exists {
			continue
		}
		if tracked[candidate] || explicitUntracked[candidate] {
			allowed = append(allowed, candidate)
			continue
		}
		excluded = append(excluded, contextpack.CoverageLimit{
			Source: candidate, Reason: "instruction is not tracked; untracked content requires explicit --include-untracked consent",
		})
	}
	result := contextpack.SnapshotInstructions(snapshot.Repository.Root, allowed, maxInstructionBytes, policy)
	confirmed := contextpack.SnapshotInstructions(snapshot.Repository.Root, allowed, maxInstructionBytes, policy)
	if !reflect.DeepEqual(result, confirmed) {
		return contextpack.InstructionSnapshot{}, errors.New("project instructions changed while freezing immutable context")
	}
	result.Excluded = append(result.Excluded, excluded...)
	sort.Slice(result.Excluded, func(i, j int) bool {
		if result.Excluded[i].Source == result.Excluded[j].Source {
			return result.Excluded[i].Reason < result.Excluded[j].Reason
		}
		return result.Excluded[i].Source < result.Excluded[j].Source
	})
	return result, nil
}

func collectorRestrictedPatterns(cfg config.Config) []string {
	metadataOnlyDefaults := map[string]struct{}{
		"vendor/**": {}, "internal/generated/**": {}, "generated/**": {},
	}
	patterns := make([]string, 0, len(cfg.RestrictedPaths)+len(cfg.Redaction.DenyPatterns))
	for _, pattern := range cfg.RestrictedPaths {
		if _, metadataOnly := metadataOnlyDefaults[pattern]; !metadataOnly {
			patterns = append(patterns, pattern)
		}
	}
	if cfg.Redaction.Enabled {
		patterns = append(patterns, cfg.Redaction.DenyPatterns...)
	}
	return patterns
}

func filepathSlash(value string) string { return strings.ReplaceAll(value, "\\", "/") }
