package scheduler

import (
	"context"
	"errors"
	"time"

	"github.com/signaturekey/zephyr/internal/codexharness/diagnostics"
)

var ErrIncomplete = errors.New("reviewer result incomplete after scheduler cancellation")

type Job struct {
	Role string
	Run  func(context.Context) Result
}

type Result struct {
	Role     string
	Category diagnostics.Category
	Err      error
}

type Scheduler struct {
	InitialLimit  int
	DegradedLimit int
}

type completed struct {
	index  int
	result Result
}

func (scheduler Scheduler) Run(ctx context.Context, jobs []Job, drainBudget time.Duration) []Result {
	results := make([]Result, len(jobs))
	if len(jobs) == 0 {
		return results
	}

	limit := scheduler.InitialLimit
	if limit <= 0 {
		limit = 4
	}
	degradedLimit := scheduler.DegradedLimit
	if degradedLimit <= 0 {
		degradedLimit = 2
	}

	workerContext, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	completedJobs := make(chan completed, len(jobs))
	next, active := 0, 0
	finished := make([]bool, len(jobs))

	start := func(index int) {
		job := jobs[index]
		active++
		go func() {
			result := Result{Role: job.Role}
			if job.Run == nil {
				result.Category = diagnostics.CategoryLifecycle
				result.Err = errors.New("scheduler job has no runner")
			} else {
				result = job.Run(workerContext)
				if result.Role == "" {
					result.Role = job.Role
				}
			}
			completedJobs <- completed{index: index, result: result}
		}()
	}

	for {
		for next < len(jobs) && active < limit && ctx.Err() == nil {
			start(next)
			next++
		}
		if active == 0 {
			break
		}

		select {
		case done := <-completedJobs:
			active--
			finished[done.index] = true
			results[done.index] = done.result
			if done.result.Category == diagnostics.CategoryRateLimit && limit > degradedLimit {
				limit = degradedLimit
			}
		case <-ctx.Done():
			cancelWorkers()
			drain(completedJobs, results, finished, &active, drainBudget)
			for index, job := range jobs {
				if !finished[index] {
					results[index] = Result{Role: job.Role, Category: diagnostics.CategoryLifecycle, Err: ErrIncomplete}
				}
			}
			return results
		}
	}
	return results
}

func drain(completedJobs <-chan completed, results []Result, finished []bool, active *int, budget time.Duration) {
	if budget <= 0 {
		return
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	for *active > 0 {
		select {
		case done := <-completedJobs:
			*active--
			finished[done.index] = true
			results[done.index] = done.result
		case <-timer.C:
			return
		}
	}
}
