package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/signaturekey/zephyr/internal/config"
	"github.com/signaturekey/zephyr/internal/contextpack"
	"github.com/signaturekey/zephyr/internal/evidence"
	"github.com/signaturekey/zephyr/internal/redaction"
	"github.com/signaturekey/zephyr/internal/routing"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/safefile"
)

const maxConfigBytes int64 = 1 << 20

func readPlan(repository, absolutePath string, maximum int64) ([]byte, error) {
	repository, err := filepath.Abs(repository)
	if err != nil {
		return nil, fmt.Errorf("resolve repository for plan: %w", err)
	}
	relative, relErr := filepath.Rel(repository, absolutePath)
	inside := relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
	policyPath := filepath.Base(absolutePath)
	root := filepath.Dir(absolutePath)
	if inside {
		policyPath = filepath.ToSlash(relative)
		root = repository
	} else {
		relative = filepath.Base(absolutePath)
	}
	if redaction.DefaultPolicy(nil).PathDenied(policyPath) {
		return nil, fmt.Errorf("plan path %q is denied by baseline secret policy", policyPath)
	}
	data, err := safefile.ReadBeneath(root, filepath.ToSlash(relative), maximum)
	if err != nil {
		return nil, fmt.Errorf("read plan %q safely: %w", absolutePath, err)
	}
	return data, nil
}

func decodeStrict[T any](path string) (T, error) {
	var zero T
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, fmt.Errorf("read JSON artifact %q: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, fmt.Errorf("decode JSON artifact %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return zero, fmt.Errorf("decode JSON artifact %q: unexpected trailing JSON value", path)
		}
		return zero, fmt.Errorf("decode JSON artifact %q trailing data: %w", path, err)
	}
	return value, nil
}

func artifact[T any](service *Service, manifest *run.Manifest, elements ...string) (T, error) {
	var zero T
	path, err := service.store.ArtifactPath(manifest.ID, elements...)
	if err != nil {
		return zero, err
	}
	return decodeStrict[T](path)
}

func loadConfig(repository string) (config.Config, error) {
	const relative = ".zephyr/config.yaml"
	data, err := safefile.ReadBeneath(repository, relative, maxConfigBytes)
	switch {
	case err == nil:
		return config.LoadBytes(data)
	case errors.Is(err, os.ErrNotExist):
		return config.Load("")
	default:
		return config.Config{}, fmt.Errorf("read project config %q beneath repository: %w", relative, err)
	}
}

func loadEffectiveConfig(service *Service, manifest *run.Manifest) (config.Config, error) {
	path, err := service.store.ArtifactPath(manifest.ID, "context", "config.json")
	if err != nil {
		return config.Config{}, err
	}
	value, err := decodeStrict[config.Config](path)
	if err == nil {
		if err := config.Validate(value); err != nil {
			return config.Config{}, fmt.Errorf("validate frozen config: %w", err)
		}
		return value, nil
	}
	if !errors.Is(unwrapPathError(err), os.ErrNotExist) {
		return config.Config{}, err
	}
	return loadConfig(manifest.Repository)
}

func redactionPolicy(cfg config.Config) redaction.Policy {
	extras := append([]string{}, cfg.RestrictedPaths...)
	if cfg.Redaction.Enabled {
		extras = append(extras, cfg.Redaction.DenyPatterns...)
	}
	return redaction.DefaultPolicy(extras)
}

func loadCoverage(service *Service, manifest *run.Manifest) (CoverageDocument, error) {
	path, err := service.store.ArtifactPath(manifest.ID, "context", "coverage-limits.json")
	if err != nil {
		return CoverageDocument{}, err
	}
	doc, err := decodeStrict[CoverageDocument](path)
	if errors.Is(unwrapPathError(err), os.ErrNotExist) {
		return CoverageDocument{Version: 1, RunID: manifest.ID, Limits: []contextpack.CoverageLimit{}}, nil
	}
	if err != nil {
		return CoverageDocument{}, err
	}
	if doc.Version != 1 || doc.RunID != manifest.ID {
		return CoverageDocument{}, fmt.Errorf("coverage artifact does not belong to run %q", manifest.ID)
	}
	if doc.Limits == nil {
		doc.Limits = []contextpack.CoverageLimit{}
	}
	return doc, nil
}

func unwrapPathError(err error) error {
	for err != nil {
		var pathError *os.PathError
		if errors.As(err, &pathError) {
			return pathError.Err
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = unwrapper.Unwrap()
	}
	return err
}

func appendCoverage(doc *CoverageDocument, source, reason string) bool {
	source = strings.TrimSpace(source)
	reason = strings.TrimSpace(reason)
	for _, item := range doc.Limits {
		if item.Source == source && item.Reason == reason {
			return false
		}
	}
	doc.Limits = append(doc.Limits, contextpack.CoverageLimit{Source: source, Reason: reason})
	return true
}

func loadPrecheckReports(service *Service, manifest *run.Manifest, route routing.Result) ([]evidence.PrecheckReport, error) {
	reports := make([]evidence.PrecheckReport, 0, len(route.Selected))
	for _, decision := range route.Selected {
		path, err := service.store.ArtifactPath(manifest.ID, "evidence", "precheck", decision.Role+".json")
		if err != nil {
			return nil, err
		}
		report, err := decodeStrict[evidence.PrecheckReport](path)
		if errors.Is(unwrapPathError(err), os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if report.RunID != manifest.ID || report.Role != decision.Role {
			return nil, fmt.Errorf("precheck artifact for %q has mismatched identity", decision.Role)
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func selectedRole(route routing.Result, role string) bool {
	for _, decision := range route.Selected {
		if decision.Role == role {
			return true
		}
	}
	return false
}

func stageState(manifest *run.Manifest, name string) (run.StageState, bool) {
	for _, stage := range manifest.Stages {
		if stage.Name == name {
			return stage.State, true
		}
	}
	return "", false
}

func ensureStage(manifest *run.Manifest, name string, allowed ...run.StageState) error {
	state, ok := stageState(manifest, name)
	if !ok {
		return fmt.Errorf("run %q has no %q stage", manifest.ID, name)
	}
	for _, candidate := range allowed {
		if state == candidate {
			return nil
		}
	}
	return fmt.Errorf("run %q stage %q is %q", manifest.ID, name, state)
}
