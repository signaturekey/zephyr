package process

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBoundedBuffersShareOneOverflowNotification(t *testing.T) {
	overflow := newOverflowNotifier()
	stdout := newBoundedBuffer(1, overflow)
	stderr := newBoundedBuffer(1, overflow)

	require.NotPanics(t, func() {
		_, _ = stdout.Write([]byte("stdout overflow"))
		_, _ = stderr.Write([]byte("stderr overflow"))
	})
}
