package trace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const Version = 1

type Status string

const (
	StatusStarted   Status = "started"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusPartial   Status = "partial"
)

type Event struct {
	Stage      string            `json:"stage"`
	Status     Status            `json:"status"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt *time.Time        `json:"finished_at,omitempty"`
	DurationMS int64             `json:"duration_ms,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Error      string            `json:"error,omitempty"`
}

type Trace struct {
	Version int     `json:"version"`
	RunID   string  `json:"run_id"`
	Events  []Event `json:"events"`
}

func Load(path, runID string) (Trace, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Trace{Version: Version, RunID: runID, Events: []Event{}}, nil
	}
	if err != nil {
		return Trace{}, fmt.Errorf("read trace: %w", err)
	}
	var result Trace
	if err := json.Unmarshal(data, &result); err != nil {
		return Trace{}, fmt.Errorf("decode trace: %w", err)
	}
	if result.Version != Version {
		return Trace{}, fmt.Errorf("unsupported trace version %d", result.Version)
	}
	if result.RunID != runID {
		return Trace{}, fmt.Errorf("trace belongs to run %q, expected %q", result.RunID, runID)
	}
	return result, nil
}

func (t *Trace) Start(stage string, now time.Time, metadata map[string]string) int {
	metadataCopy := make(map[string]string, len(metadata))
	for key, value := range metadata {
		metadataCopy[key] = value
	}
	t.Events = append(t.Events, Event{
		Stage:     stage,
		Status:    StatusStarted,
		StartedAt: now.UTC(),
		Metadata:  metadataCopy,
	})
	return len(t.Events) - 1
}

func (t *Trace) Started(stage string) (int, error) {
	index := -1
	for i := range t.Events {
		if t.Events[i].Stage != stage || t.Events[i].Status != StatusStarted {
			continue
		}
		if index >= 0 {
			return -1, fmt.Errorf("multiple started trace events for stage %q", stage)
		}
		index = i
	}
	if index < 0 {
		return -1, fmt.Errorf("no started trace event for stage %q", stage)
	}
	return index, nil
}

func (t *Trace) Finish(index int, status Status, now time.Time, safeError string) error {
	if index < 0 || index >= len(t.Events) {
		return fmt.Errorf("trace event index %d out of range", index)
	}
	if status != StatusCompleted && status != StatusFailed && status != StatusPartial {
		return fmt.Errorf("invalid terminal trace status %q", status)
	}
	finished := now.UTC()
	event := &t.Events[index]
	event.Status = status
	event.FinishedAt = &finished
	event.DurationMS = max(0, finished.Sub(event.StartedAt).Milliseconds())
	event.Error = safeError
	return nil
}

func Save(path string, value Trace) error {
	if value.Version == 0 {
		value.Version = Version
	}
	if value.RunID == "" {
		return errors.New("trace run ID is required")
	}
	if value.Events == nil {
		value.Events = []Event{}
	}
	for i := range value.Events {
		if value.Events[i].Metadata == nil {
			continue
		}
		keys := make([]string, 0, len(value.Events[i].Metadata))
		for key := range value.Events[i].Metadata {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ordered := make(map[string]string, len(keys))
		for _, key := range keys {
			ordered[key] = value.Events[i].Metadata[key]
		}
		value.Events[i].Metadata = ordered
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode trace: %w", err)
	}
	data = append(data, '\n')
	return atomicWrite(path, data, 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create trace directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".trace-*")
	if err != nil {
		return fmt.Errorf("create temporary trace: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("set trace mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write trace: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync trace: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close trace: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace trace: %w", err)
	}
	return nil
}
