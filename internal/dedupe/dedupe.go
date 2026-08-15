package dedupe

import (
	"sort"
	"strings"

	"github.com/signaturekey/zephyr/internal/protocol"
)

type Group struct {
	Canonical    protocol.CandidateFinding   `json:"canonical"`
	Members      []protocol.CandidateFinding `json:"members"`
	SourceRoles  []string                    `json:"source_roles"`
	DuplicateIDs []string                    `json:"duplicate_ids"`
}

func GroupFindings(findings []protocol.CandidateFinding) []Group {
	ordered := append([]protocol.CandidateFinding(nil), findings...)
	sort.SliceStable(ordered, func(i, j int) bool { return better(ordered[i], ordered[j]) })

	groups := make([]Group, 0, len(ordered))
	for _, finding := range ordered {
		groupIndex := -1
		for i := range groups {
			if Equivalent(groups[i].Canonical, finding) {
				groupIndex = i
				break
			}
		}
		if groupIndex == -1 {
			groups = append(groups, Group{
				Canonical:    finding,
				Members:      []protocol.CandidateFinding{finding},
				SourceRoles:  []string{finding.Role},
				DuplicateIDs: []string{},
			})
			continue
		}
		group := &groups[groupIndex]
		group.Members = append(group.Members, finding)
		group.DuplicateIDs = append(group.DuplicateIDs, finding.ID)
		group.SourceRoles = appendUnique(group.SourceRoles, finding.Role)
	}
	for i := range groups {
		sort.Strings(groups[i].SourceRoles)
		sort.Strings(groups[i].DuplicateIDs)
	}
	sort.SliceStable(groups, func(i, j int) bool { return better(groups[i].Canonical, groups[j].Canonical) })
	return groups
}

func Equivalent(left, right protocol.CandidateFinding) bool {
	if normalize(left.Category) != normalize(right.Category) ||
		normalize(left.Evidence.ViolatedInvariant) != normalize(right.Evidence.ViolatedInvariant) ||
		normalize(left.Evidence.ExecutionPath) != normalize(right.Evidence.ExecutionPath) ||
		normalize(left.Impact) != normalize(right.Impact) {
		return false
	}
	return locationsOverlap(left.Location, right.Location)
}

func locationsOverlap(left, right protocol.FindingLocation) bool {
	if left.IsCode() != right.IsCode() || left.IsArtifact() != right.IsArtifact() {
		return false
	}
	if left.IsCode() {
		if normalizePath(left.File) != normalizePath(right.File) {
			return false
		}
	} else if normalizePath(left.Artifact) != normalizePath(right.Artifact) || normalize(left.Section) != normalize(right.Section) {
		return false
	}
	leftStart, leftEnd := lineRange(left)
	rightStart, rightEnd := lineRange(right)
	if leftStart == 0 || rightStart == 0 {
		return true
	}
	return leftStart <= rightEnd && rightStart <= leftEnd
}

func lineRange(location protocol.FindingLocation) (int, int) {
	start := location.LineStart
	end := location.LineEnd
	if end == 0 {
		end = start
	}
	return start, end
}

func better(left, right protocol.CandidateFinding) bool {
	if left.Severity.Rank() != right.Severity.Rank() {
		return left.Severity.Rank() < right.Severity.Rank()
	}
	if left.Confidence != right.Confidence {
		return left.Confidence > right.Confidence
	}
	return left.ID < right.ID
}

func normalize(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func normalizePath(value string) string {
	return strings.TrimPrefix(strings.ReplaceAll(strings.ToLower(value), "\\", "/"), "./")
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
