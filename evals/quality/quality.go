package quality

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type finding struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
}

type humanFinding struct {
	ID       string          `json:"id"`
	Severity string          `json:"severity"`
	Category string          `json:"category"`
	Summary  string          `json:"summary"`
	Path     json.RawMessage `json:"path"`
	Line     json.RawMessage `json:"line"`
}

type record struct {
	CaseID   string `json:"case_id"`
	Baseline struct {
		HumanFindings []humanFinding `json:"human_findings"`
	} `json:"baseline"`
	ZephyrRun struct {
		Findings []finding `json:"findings"`
	} `json:"zephyr_run"`
	Comparison struct {
		Matched []struct {
			HumanID   string   `json:"human_id"`
			ZephyrIDs []string `json:"zephyr_ids"`
		} `json:"matched"`
		MissedHumanIDs []string `json:"missed_human_ids"`
		ZephyrOnly     []struct {
			ZephyrID    string `json:"zephyr_id"`
			Disposition string `json:"disposition"`
		} `json:"zephyr_only"`
	} `json:"comparison"`
}

type Metrics struct {
	Cases                int
	HumanFindings        int
	MatchedHumanFindings int
	ZephyrFindings       int
	FalsePositives       int
	SeverityComparisons  int
	SeverityMatches      int
}

func (metrics Metrics) Recall() float64 {
	return ratio(metrics.MatchedHumanFindings, metrics.HumanFindings)
}

func (metrics Metrics) FalsePositiveRate() float64 {
	return ratio(metrics.FalsePositives, metrics.ZephyrFindings)
}

func (metrics Metrics) SeverityAgreement() float64 {
	return ratio(metrics.SeverityMatches, metrics.SeverityComparisons)
}

type Comparison struct {
	Baseline    Metrics
	Candidate   Metrics
	Regressions []string
}

func CompareDirectories(baselineDir, candidateDir string) (Comparison, error) {
	baseline, err := loadDirectory(baselineDir)
	if err != nil {
		return Comparison{}, fmt.Errorf("load baseline: %w", err)
	}
	candidate, err := loadDirectory(candidateDir)
	if err != nil {
		return Comparison{}, fmt.Errorf("load candidate: %w", err)
	}
	if err := sameCases(baseline, candidate); err != nil {
		return Comparison{}, err
	}
	result := Comparison{Baseline: calculate(baseline), Candidate: calculate(candidate)}
	if result.Candidate.Recall() < result.Baseline.Recall() {
		result.Regressions = append(result.Regressions, "confirmed-finding recall decreased")
	}
	if result.Candidate.FalsePositiveRate() > result.Baseline.FalsePositiveRate() {
		result.Regressions = append(result.Regressions, "false-positive rate increased")
	}
	if result.Candidate.SeverityAgreement() < result.Baseline.SeverityAgreement() {
		result.Regressions = append(result.Regressions, "P0-P3 severity agreement decreased")
	}
	return result, nil
}

func loadDirectory(dir string) (map[string]record, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no JSON eval records in %q", dir)
	}
	records := make(map[string]record, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var item record
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		if item.CaseID == "" {
			return nil, fmt.Errorf("%s has no case_id", path)
		}
		if _, exists := records[item.CaseID]; exists {
			return nil, fmt.Errorf("duplicate case_id %q", item.CaseID)
		}
		records[item.CaseID] = item
	}
	return records, nil
}

func sameCases(baseline, candidate map[string]record) error {
	if len(baseline) != len(candidate) {
		return fmt.Errorf("quality comparison requires identical case sets")
	}
	for id, baselineRecord := range baseline {
		candidateRecord, ok := candidate[id]
		if !ok || !sameHumanBaseline(baselineRecord, candidateRecord) {
			return fmt.Errorf("quality comparison requires the same human baseline for case %q", id)
		}
	}
	return nil
}

func sameHumanBaseline(left, right record) bool {
	leftFindings := append([]humanFinding(nil), left.Baseline.HumanFindings...)
	rightFindings := append([]humanFinding(nil), right.Baseline.HumanFindings...)
	sort.Slice(leftFindings, func(i, j int) bool { return leftFindings[i].ID < leftFindings[j].ID })
	sort.Slice(rightFindings, func(i, j int) bool { return rightFindings[i].ID < rightFindings[j].ID })
	if len(leftFindings) != len(rightFindings) {
		return false
	}
	for index := range leftFindings {
		leftFinding, rightFinding := leftFindings[index], rightFindings[index]
		if leftFinding.ID != rightFinding.ID || leftFinding.Severity != rightFinding.Severity ||
			leftFinding.Category != rightFinding.Category || leftFinding.Summary != rightFinding.Summary ||
			string(leftFinding.Path) != string(rightFinding.Path) || string(leftFinding.Line) != string(rightFinding.Line) {
			return false
		}
	}
	return true
}

func calculate(records map[string]record) Metrics {
	var result Metrics
	result.Cases = len(records)
	for _, item := range records {
		result.HumanFindings += len(item.Baseline.HumanFindings)
		result.MatchedHumanFindings += len(item.Comparison.Matched)
		result.ZephyrFindings += len(item.ZephyrRun.Findings)
		humanSeverity := make(map[string]string, len(item.Baseline.HumanFindings))
		for _, human := range item.Baseline.HumanFindings {
			humanSeverity[human.ID] = human.Severity
		}
		zephyrSeverity := make(map[string]string, len(item.ZephyrRun.Findings))
		for _, zephyr := range item.ZephyrRun.Findings {
			zephyrSeverity[zephyr.ID] = zephyr.Severity
		}
		for _, match := range item.Comparison.Matched {
			result.SeverityComparisons++
			for _, id := range match.ZephyrIDs {
				if zephyrSeverity[id] == humanSeverity[match.HumanID] {
					result.SeverityMatches++
					break
				}
			}
		}
		for _, extra := range item.Comparison.ZephyrOnly {
			if extra.Disposition == "false-positive" {
				result.FalsePositives++
			}
		}
	}
	return result
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 1
	}
	return float64(numerator) / float64(denominator)
}
