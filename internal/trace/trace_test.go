package trace

import (
	"path/filepath"
	"testing"
	"time"
)

func TestTraceRoundTrip(t *testing.T) {
	start := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	value := Trace{Version: Version, RunID: "run-1", Events: []Event{}}
	index := value.Start("collect", start, map[string]string{"source": "working-tree"})
	if err := value.Finish(index, StatusCompleted, start.Add(1500*time.Millisecond), ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "trace.json")
	if err := Save(path, value); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 || got.Events[0].DurationMS != 1500 || got.Events[0].Status != StatusCompleted {
		t.Fatalf("unexpected trace: %#v", got)
	}
}

func TestFinishRejectsInvalidState(t *testing.T) {
	value := Trace{Version: Version, RunID: "run"}
	if err := value.Finish(0, StatusCompleted, time.Now(), ""); err == nil {
		t.Fatal("expected invalid index error")
	}
	index := value.Start("stage", time.Now(), nil)
	if err := value.Finish(index, StatusStarted, time.Now(), ""); err == nil {
		t.Fatal("expected invalid terminal status error")
	}
}
