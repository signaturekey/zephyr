package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

const defaultOutputLimit int64 = 1024 * 1024

var (
	ErrOutputLimit  = errors.New("process output limit exceeded")
	errRequestTimer = errors.New("process request timeout")
)

type Request struct {
	Path        string
	Args        []string
	Dir         string
	Env         []string
	Stdin       []byte
	Timeout     time.Duration
	OutputLimit int64
}

type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
	TimedOut bool
}

type Runner interface {
	Run(context.Context, Request) (Result, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(parent context.Context, request Request) (Result, error) {
	result := Result{ExitCode: -1}
	if parent == nil {
		return result, errors.New("run process: context is nil")
	}
	if request.Path == "" {
		return result, errors.New("run process: path is required")
	}

	runContext, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	var timeout *time.Timer
	if request.Timeout > 0 {
		timeout = time.AfterFunc(request.Timeout, func() { cancel(errRequestTimer) })
		defer timeout.Stop()
	}

	limit := request.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	overflow := newOverflowNotifier()
	stdout := newBoundedBuffer(limit, overflow)
	stderr := newBoundedBuffer(limit, overflow)

	cmd := exec.CommandContext(runContext, request.Path, request.Args...)
	cmd.Path = request.Path
	cmd.Args = append([]string{request.Path}, request.Args...)
	cmd.Dir = request.Dir
	cmd.Env = append([]string{}, request.Env...)
	cmd.Stdin = bytes.NewReader(request.Stdin)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	configureProcessGroup(cmd)
	cmd.Cancel = func() error {
		return terminateProcessGroup(cmd.Process.Pid)
	}

	if err := cmd.Start(); err != nil {
		return result, fmt.Errorf("start process %q: %w", request.Path, err)
	}
	go func() {
		select {
		case <-overflow.channel:
			cancel(ErrOutputLimit)
		case <-runContext.Done():
		}
	}()

	waitErr := cmd.Wait()
	result.Stdout = stdout.Bytes()
	result.Stderr = stderr.Bytes()
	result.ExitCode = cmd.ProcessState.ExitCode()
	cause := context.Cause(runContext)
	switch {
	case errors.Is(cause, ErrOutputLimit):
		return result, ErrOutputLimit
	case errors.Is(cause, errRequestTimer):
		result.TimedOut = true
		return result, nil
	case cause != nil:
		return result, nil
	case waitErr == nil:
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) {
		return result, nil
	}
	return result, fmt.Errorf("wait for process %q: %w", request.Path, waitErr)
}

type boundedBuffer struct {
	mu       sync.Mutex
	data     []byte
	limit    int64
	overflow *overflowNotifier
}

type overflowNotifier struct {
	channel chan struct{}
	once    sync.Once
}

func newOverflowNotifier() *overflowNotifier {
	return &overflowNotifier{channel: make(chan struct{})}
}

func (notifier *overflowNotifier) notify() {
	notifier.once.Do(func() { close(notifier.channel) })
}

func newBoundedBuffer(limit int64, overflow *overflowNotifier) *boundedBuffer {
	return &boundedBuffer{limit: limit, overflow: overflow}
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	remaining := b.limit - int64(len(b.data))
	if remaining > 0 {
		keep := int64(len(data))
		if keep > remaining {
			keep = remaining
		}
		b.data = append(b.data, data[:keep]...)
	}
	overLimit := int64(len(data)) > remaining
	b.mu.Unlock()
	if overLimit {
		b.overflow.notify()
	}
	return len(data), nil
}

func (b *boundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}
