package budget

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChildReservesCleanupAndUsesEarlierParentDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), CleanupReserve+100*time.Millisecond)
	defer cancel()
	child, stop, err := Child(parent, time.Minute)
	require.NoError(t, err)
	defer stop()
	deadline, ok := child.Deadline()
	require.True(t, ok)
	assert.LessOrEqual(t, time.Until(deadline), 120*time.Millisecond)
}

func TestChildRejectsExhaustedBudget(t *testing.T) {
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(CleanupReserve))
	defer cancel()
	_, _, err := Child(parent, time.Second)
	require.Error(t, err)
}
