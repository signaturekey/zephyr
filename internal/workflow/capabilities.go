package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/signaturekey/zephyr/internal/run"
	"github.com/signaturekey/zephyr/internal/trace"
)

const capabilityDocumentVersion = 1

var requiredCapabilitySources = []CapabilitySource{
	CapabilityJira,
	CapabilityConfluence,
	CapabilityBitbucket,
}

func (source CapabilitySource) Validate() error {
	switch source {
	case CapabilityJira, CapabilityConfluence, CapabilityBitbucket:
		return nil
	default:
		return fmt.Errorf("unknown capability source %q", source)
	}
}

func (status CapabilityStatus) Validate() error {
	switch status {
	case CapabilityAvailable, CapabilityUnavailable, CapabilityNotRequired:
		return nil
	default:
		return fmt.Errorf("unknown capability status %q", status)
	}
}

func (service *Service) SetCapability(ctx context.Context, options CapabilitySetOptions) (result CapabilityDocument, returnErr error) {
	if err := requireService(service); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	options.Source = CapabilitySource(strings.ToLower(strings.TrimSpace(string(options.Source))))
	options.Status = CapabilityStatus(strings.ToLower(strings.TrimSpace(string(options.Status))))
	options.Reason = strings.TrimSpace(options.Reason)
	if err := options.Source.Validate(); err != nil {
		return result, err
	}
	if err := options.Status.Validate(); err != nil {
		return result, err
	}
	if options.Status != CapabilityAvailable && options.Reason == "" {
		return result, fmt.Errorf("capability status %q requires a reason", options.Status)
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
		return result, fmt.Errorf("capability preflight requires a completed Git collection: %w", err)
	}
	if err := ensureStage(manifest, "route", run.StagePending); err != nil {
		return result, fmt.Errorf("capability preflight must be frozen before routing: %w", err)
	}
	if err := ensureStage(manifest, "review", run.StagePending); err != nil {
		return result, fmt.Errorf("capability preflight cannot change after reviewers start: %w", err)
	}
	cfg, err := loadEffectiveConfig(service, manifest)
	if err != nil {
		return result, err
	}
	options.Reason = redactionPolicy(cfg).Text(options.Reason)
	event, err := service.startTrace(manifest, "capability", map[string]string{
		"source": string(options.Source),
		"status": string(options.Status),
	})
	if err != nil {
		return result, err
	}
	defer func() {
		status := trace.StatusCompleted
		if returnErr != nil {
			status = trace.StatusFailed
		}
		if err := event.finish(status, returnErr); returnErr == nil && err != nil {
			returnErr = err
		}
	}()

	doc, err := loadCapabilities(service, manifest)
	if err != nil {
		return result, err
	}
	record := CapabilityRecord{Source: options.Source, Status: options.Status, Reason: options.Reason}
	replaced := false
	for index := range doc.Capabilities {
		if doc.Capabilities[index].Source == options.Source {
			doc.Capabilities[index] = record
			replaced = true
			break
		}
	}
	if !replaced {
		doc.Capabilities = append(doc.Capabilities, record)
	}
	sort.Slice(doc.Capabilities, func(i, j int) bool {
		return doc.Capabilities[i].Source < doc.Capabilities[j].Source
	})
	if _, err := service.store.WriteJSON(ctx, manifest.ID, doc, "context", "capabilities.json"); err != nil {
		return result, err
	}
	return doc, nil
}

func loadCapabilities(service *Service, manifest *run.Manifest) (CapabilityDocument, error) {
	path, err := service.store.ArtifactPath(manifest.ID, "context", "capabilities.json")
	if err != nil {
		return CapabilityDocument{}, err
	}
	doc, err := decodeStrict[CapabilityDocument](path)
	if errors.Is(unwrapPathError(err), os.ErrNotExist) {
		return CapabilityDocument{
			Version: capabilityDocumentVersion, RunID: manifest.ID, Capabilities: []CapabilityRecord{},
		}, nil
	}
	if err != nil {
		return CapabilityDocument{}, err
	}
	if doc.Version != capabilityDocumentVersion || doc.RunID != manifest.ID {
		return CapabilityDocument{}, fmt.Errorf("capability artifact does not belong to run %q", manifest.ID)
	}
	if doc.Capabilities == nil {
		doc.Capabilities = []CapabilityRecord{}
	}
	seen := make(map[CapabilitySource]struct{}, len(doc.Capabilities))
	for _, record := range doc.Capabilities {
		if err := record.Source.Validate(); err != nil {
			return CapabilityDocument{}, err
		}
		if err := record.Status.Validate(); err != nil {
			return CapabilityDocument{}, err
		}
		if record.Status != CapabilityAvailable && strings.TrimSpace(record.Reason) == "" {
			return CapabilityDocument{}, fmt.Errorf("capability %q status %q has no reason", record.Source, record.Status)
		}
		if _, duplicate := seen[record.Source]; duplicate {
			return CapabilityDocument{}, fmt.Errorf("capability source %q is duplicated", record.Source)
		}
		seen[record.Source] = struct{}{}
	}
	return doc, nil
}

func validateCapabilityPreflight(doc CapabilityDocument) error {
	recorded := make(map[CapabilitySource]struct{}, len(doc.Capabilities))
	for _, record := range doc.Capabilities {
		recorded[record.Source] = struct{}{}
	}
	missing := make([]string, 0, len(requiredCapabilitySources))
	for _, source := range requiredCapabilitySources {
		if _, ok := recorded[source]; !ok {
			missing = append(missing, string(source))
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf(
			"capability preflight incomplete: record %s with `zephyr context capability` before routing",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

func requireAvailableCapability(doc CapabilityDocument, source CapabilitySource) error {
	if err := source.Validate(); err != nil {
		return err
	}
	for _, record := range doc.Capabilities {
		if record.Source != source {
			continue
		}
		if record.Status != CapabilityAvailable {
			return fmt.Errorf("business context source %q requires capability status %q, got %q", source, CapabilityAvailable, record.Status)
		}
		return nil
	}
	return fmt.Errorf("business context source %q requires a recorded capability before import", source)
}
