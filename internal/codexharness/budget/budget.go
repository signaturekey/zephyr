package budget

import (
	"context"
	"errors"
	"time"
)

const (
	DoctorTotal     = 3 * time.Minute
	ReviewTotal     = 2 * time.Hour
	CoreCall        = 60 * time.Second
	ProbeSmokeOuter = 130 * time.Second
	DispatchOuter   = 31 * time.Minute
	CleanupReserve  = 15 * time.Second
)

var ErrExhausted = errors.New("operation budget exhausted")

func Child(parent context.Context, stageLimit time.Duration) (context.Context, context.CancelFunc, error) {
	if parent == nil || stageLimit <= 0 {
		return nil, nil, ErrExhausted
	}
	now := time.Now()
	limit := now.Add(stageLimit)
	if deadline, ok := parent.Deadline(); ok {
		available := deadline.Add(-CleanupReserve)
		if !available.After(now) {
			return nil, nil, ErrExhausted
		}
		if available.Before(limit) {
			limit = available
		}
	}
	if !limit.After(now) {
		return nil, nil, ErrExhausted
	}
	child, cancel := context.WithDeadline(parent, limit)
	return child, cancel, nil
}
