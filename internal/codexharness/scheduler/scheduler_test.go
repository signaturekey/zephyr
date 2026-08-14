package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/signaturekey/zephyr/internal/codexharness/diagnostics"
)

func TestSchedulerReducesFutureStartsAfterRateLimit(t *testing.T) {
	t.Parallel()

	started := make(chan string, 8)
	release := make(chan struct{})
	jobs := make([]Job, 6)
	for index := range jobs {
		role := string(rune('a' + index))
		jobs[index] = Job{Role: role, Run: func(context.Context) Result {
			started <- role
			if role == "a" {
				return Result{Role: role, Category: diagnostics.CategoryRateLimit}
			}
			<-release
			return Result{Role: role}
		}}
	}

	done := make(chan []Result, 1)
	go func() {
		done <- Scheduler{InitialLimit: 4, DegradedLimit: 2}.Run(context.Background(), jobs, time.Second)
	}()

	for range 4 {
		<-started
	}
	select {
	case role := <-started:
		t.Fatalf("started %q before an initial worker completed", role)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	results := <-done
	require.Len(t, results, 6)
}

func TestSchedulerReturnsResultsInRoutingOrderAndRunsEachJobOnce(t *testing.T) {
	t.Parallel()

	var calls [3]atomic.Int32
	jobs := []Job{
		{Role: "first", Run: func(context.Context) Result {
			calls[0].Add(1)
			time.Sleep(20 * time.Millisecond)
			return Result{Role: "first"}
		}},
		{Role: "second", Run: func(context.Context) Result { calls[1].Add(1); return Result{Role: "second"} }},
		{Role: "third", Run: func(context.Context) Result {
			calls[2].Add(1)
			time.Sleep(10 * time.Millisecond)
			return Result{Role: "third"}
		}},
	}

	results := (Scheduler{InitialLimit: 3, DegradedLimit: 2}).Run(context.Background(), jobs, time.Second)
	require.Equal(t, []string{"first", "second", "third"}, []string{results[0].Role, results[1].Role, results[2].Role})
	for index := range calls {
		require.Equal(t, int32(1), calls[index].Load())
	}
}

func TestSchedulerCancellationStopsStartsAndMarksUnfinishedJobsIncomplete(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	neverStops := make(chan struct{})
	jobs := []Job{
		{Role: "stuck", Run: func(context.Context) Result { started <- struct{}{}; <-neverStops; return Result{Role: "stuck"} }},
		{Role: "queued", Run: func(context.Context) Result { t.Fatal("queued job started after cancellation"); return Result{} }},
	}
	done := make(chan []Result, 1)
	go func() { done <- Scheduler{InitialLimit: 1, DegradedLimit: 1}.Run(ctx, jobs, 20*time.Millisecond) }()
	<-started
	cancel()

	select {
	case results := <-done:
		require.Len(t, results, 2)
		for _, result := range results {
			require.ErrorIs(t, result.Err, ErrIncomplete)
			require.Equal(t, diagnostics.CategoryLifecycle, result.Category)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler waited forever for a worker that ignored cancellation")
	}
}

func TestSchedulerDoesNotRetryTerminalResults(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	result := (Scheduler{}).Run(context.Background(), []Job{{Role: "one", Run: func(context.Context) Result {
		calls.Add(1)
		return Result{Role: "one", Category: diagnostics.CategoryRateLimit, Err: errors.New("terminal")}
	}}}, time.Second)
	require.Equal(t, int32(1), calls.Load())
	require.Len(t, result, 1)
}
