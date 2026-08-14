package evidence

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/schema"
)

type GateInput struct {
	Version int                `json:"version"`
	RunID   string             `json:"run_id"`
	Items   []GateEvidenceItem `json:"items"`
}

type GateEvidenceItem struct {
	CandidateID string                 `json:"candidate_id"`
	Location    schema.FindingLocation `json:"location"`
	DiffHunks   []string               `json:"diff_hunks"`
}

func BuildGateInput(candidates CandidateSet, packet contextpack.Packet) (GateInput, error) {
	if candidates.Version != schema.ProtocolVersion || packet.Version != contextpack.Version {
		return GateInput{}, errors.New("candidate set and packet versions must match the core protocol")
	}
	if candidates.RunID == "" || candidates.RunID != packet.RunID {
		return GateInput{}, fmt.Errorf("candidate set run_id %q does not match packet run_id %q", candidates.RunID, packet.RunID)
	}
	result := GateInput{Version: schema.ProtocolVersion, RunID: candidates.RunID, Items: []GateEvidenceItem{}}
	if len(candidates.Findings) == 0 {
		return result, nil
	}
	hunks := extractDiffHunks(packet.Diff.Full)
	for _, candidate := range candidates.Findings {
		if strings.TrimSpace(candidate.ID) == "" {
			return GateInput{}, errors.New("candidate ID is required")
		}
		if !candidate.Location.IsCode() || candidate.Location.IsArtifact() {
			return GateInput{}, fmt.Errorf("candidate %q must have only a code location", candidate.ID)
		}
		lineEnd := candidate.Location.LineEnd
		if lineEnd == 0 {
			lineEnd = candidate.Location.LineStart
		}
		if candidate.Location.LineStart < 1 || lineEnd < candidate.Location.LineStart {
			return GateInput{}, fmt.Errorf("candidate %q has an invalid code location range", candidate.ID)
		}
		location := candidate.Location
		location.LineEnd = lineEnd
		item := GateEvidenceItem{CandidateID: candidate.ID, Location: location, DiffHunks: []string{}}
		for _, hunk := range hunks {
			if hunk.path != location.File || !rangesOverlap(location.LineStart, location.LineEnd, hunk.start, hunk.end) {
				continue
			}
			item.DiffHunks = append(item.DiffHunks, hunk.text)
		}
		if len(item.DiffHunks) == 0 {
			return GateInput{}, fmt.Errorf("candidate %q location is absent from the frozen diff", candidate.ID)
		}
		result.Items = append(result.Items, item)
	}
	sort.SliceStable(result.Items, func(i, j int) bool { return result.Items[i].CandidateID < result.Items[j].CandidateID })
	return result, nil
}

func rangesOverlap(leftStart, leftEnd, rightStart, rightEnd int) bool {
	return leftStart <= rightEnd && rightStart <= leftEnd
}
