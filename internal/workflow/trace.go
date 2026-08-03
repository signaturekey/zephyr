package workflow

import (
	"fmt"
	"time"

	"github.com/signaturekey/zephyr/internal/redaction"
	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/trace"
)

type traceEvent struct {
	path   string
	trace  trace.Trace
	index  int
	now    func() time.Time
	policy redaction.Policy
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
	return &traceEvent{path: path, trace: value, index: index, now: now, policy: policy}, nil
}

func (event *traceEvent) finish(status trace.Status, operationErr error) error {
	if event == nil {
		return nil
	}
	safeError := ""
	if operationErr != nil {
		safeError = event.policy.Text(operationErr.Error())
	}
	if err := event.trace.Finish(event.index, status, event.now(), safeError); err != nil {
		return err
	}
	if err := trace.Save(event.path, event.trace); err != nil {
		return fmt.Errorf("save trace: %w", err)
	}
	return nil
}
