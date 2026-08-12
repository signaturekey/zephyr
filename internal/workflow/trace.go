package workflow

import (
	"fmt"
	"time"

	"github.com/signaturekey/zephyr/internal/redaction"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/trace"
)

type traceEvent struct {
	path     string
	runID    string
	index    int
	now      func() time.Time
	policy   redaction.Policy
	metadata map[string]string
}

func (service *Service) startTrace(manifest *run.Manifest, stage string, metadata map[string]string) (*traceEvent, error) {
	path, err := service.store.ArtifactPath(manifest.ID, "trace.json")
	if err != nil {
		return nil, err
	}
	value, err := trace.Load(path, manifest.ID)
	if err != nil {
		return nil, err
	}
	now := service.now
	policy := redaction.DefaultPolicy(nil)
	if cfg, configErr := loadEffectiveConfig(service, manifest); configErr == nil {
		policy = redactionPolicy(cfg)
	}
	safeMetadata := make(map[string]string, len(metadata))
	for key, item := range metadata {
		safeMetadata[key] = policy.Text(item)
	}
	index := value.Start(stage, now(), safeMetadata)
	if err := trace.Save(path, value); err != nil {
		return nil, err
	}
	return &traceEvent{path: path, runID: manifest.ID, index: index, now: now, policy: policy, metadata: map[string]string{}}, nil
}

func (service *Service) resumeTrace(manifest *run.Manifest, stage string) (*traceEvent, error) {
	path, err := service.store.ArtifactPath(manifest.ID, "trace.json")
	if err != nil {
		return nil, err
	}
	value, err := trace.Load(path, manifest.ID)
	if err != nil {
		return nil, err
	}
	index, err := value.Started(stage)
	if err != nil {
		return nil, err
	}
	policy := redaction.DefaultPolicy(nil)
	if cfg, configErr := loadEffectiveConfig(service, manifest); configErr == nil {
		policy = redactionPolicy(cfg)
	}
	return &traceEvent{
		path: path, runID: manifest.ID, index: index, now: service.now, policy: policy, metadata: map[string]string{},
	}, nil
}

func (event *traceEvent) setMetadata(key, value string) {
	if event == nil {
		return
	}
	event.metadata[key] = event.policy.Text(value)
}

func (event *traceEvent) finish(status trace.Status, operationErr error) error {
	if event == nil {
		return nil
	}
	safeError := ""
	if operationErr != nil {
		safeError = event.policy.Text(operationErr.Error())
	}
	value, err := trace.Load(event.path, event.runID)
	if err != nil {
		return err
	}
	if event.index < 0 || event.index >= len(value.Events) {
		return fmt.Errorf("trace event index %d out of range", event.index)
	}
	if value.Events[event.index].Metadata == nil {
		value.Events[event.index].Metadata = map[string]string{}
	}
	for key, item := range event.metadata {
		value.Events[event.index].Metadata[key] = item
	}
	if err := value.Finish(event.index, status, event.now(), safeError); err != nil {
		return err
	}
	if err := trace.Save(event.path, value); err != nil {
		return fmt.Errorf("save trace: %w", err)
	}
	return nil
}
