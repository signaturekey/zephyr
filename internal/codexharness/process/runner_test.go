//go:build unix

package process_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	harnessprocess "github.com/signaturekey/zephyr/internal/codexharness/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const helperMarker = "ZEPHYR_PROCESS_HELPER"

func TestRunnerPreservesArgvAndStdinWithoutShellInterpretation(t *testing.T) {
	request := helperRequest("echo", "argument with spaces", "$(touch nope)", "semi;colon", "quote'\"")
	request.Stdin = []byte("stdin\x00bytes\n")

	result, err := (harnessprocess.ExecRunner{}).Run(context.Background(), request)

	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "argument with spaces\n$(touch nope)\nsemi;colon\nquote'\"\n--stdin--\nstdin\x00bytes\n", string(result.Stdout))
	assert.Empty(t, result.Stderr)
}

func TestRunnerCapturesBoundedStdoutAndStderrIndependently(t *testing.T) {
	request := helperRequest("bounded")
	request.OutputLimit = 8

	result, err := (harnessprocess.ExecRunner{}).Run(context.Background(), request)

	require.NoError(t, err)
	assert.Equal(t, []byte("12345678"), result.Stdout)
	assert.Equal(t, []byte("abcdefgh"), result.Stderr)
}

func TestRunnerRepresentsNonZeroExitWithoutReturningProcessStderrInError(t *testing.T) {
	result, err := (harnessprocess.ExecRunner{}).Run(context.Background(), helperRequest("exit", "23", "private stderr"))

	require.NoError(t, err)
	assert.Equal(t, 23, result.ExitCode)
	assert.Equal(t, []byte("private stderr"), result.Stderr)
}

func TestRunnerReturnsStartFailure(t *testing.T) {
	result, err := (harnessprocess.ExecRunner{}).Run(context.Background(), harnessprocess.Request{
		Path: "/path/that/does/not/exist/zephyr-helper",
	})

	require.Error(t, err)
	assert.Equal(t, -1, result.ExitCode)
	assert.NotContains(t, err.Error(), "private stderr")
}

func TestRunnerTimesOutAndTerminatesTermIgnoringDescendant(t *testing.T) {
	pidFile := t.TempDir() + "/descendant.pid"
	request := helperRequest("parent", pidFile)
	request.Timeout = 150 * time.Millisecond

	started := time.Now()
	result, err := (harnessprocess.ExecRunner{}).Run(context.Background(), request)

	require.NoError(t, err)
	assert.True(t, result.TimedOut)
	assert.Less(t, time.Since(started), 2*time.Second)
	assertProcessGone(t, readPID(t, pidFile))
}

func TestRunnerCancellationTerminatesProcessGroupWithoutReportingTimeout(t *testing.T) {
	pidFile := t.TempDir() + "/descendant.pid"
	request := helperRequest("parent", pidFile)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		require.Eventually(t, func() bool {
			_, err := os.Stat(pidFile)
			return err == nil
		}, time.Second, 10*time.Millisecond)
		cancel()
	}()

	result, err := (harnessprocess.ExecRunner{}).Run(ctx, request)

	require.NoError(t, err)
	assert.False(t, result.TimedOut)
	assertProcessGone(t, readPID(t, pidFile))
}

func TestRunnerOutputLimitTerminatesWholeGroupBeforeReturning(t *testing.T) {
	temp := t.TempDir()
	pidFile := temp + "/descendant.pid"
	growthFile := temp + "/growth.log"
	request := helperRequest("overflow", pidFile, growthFile)
	request.OutputLimit = 128
	request.Timeout = 5 * time.Second

	result, err := (harnessprocess.ExecRunner{}).Run(context.Background(), request)

	require.Error(t, err)
	assert.ErrorIs(t, err, harnessprocess.ErrOutputLimit)
	assert.NotContains(t, err.Error(), strings.Repeat("x", 32))
	assert.LessOrEqual(t, len(result.Stdout), 128)
	assertProcessGone(t, readPID(t, pidFile))
	before := fileSize(t, growthFile)
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, before, fileSize(t, growthFile), "descendant kept writing after Run returned")
}

func helperRequest(mode string, args ...string) harnessprocess.Request {
	return harnessprocess.Request{
		Path: os.Args[0],
		Args: append([]string{"-test.run=^TestProcessHelper$", "--", mode}, args...),
		Env:  []string{helperMarker + "=1"},
	}
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv(helperMarker) != "1" {
		return
	}
	separator := 0
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index + 1
			break
		}
	}
	if separator == 0 || separator >= len(os.Args) {
		os.Exit(97)
	}
	args := os.Args[separator:]
	switch args[0] {
	case "echo":
		for _, argument := range args[1:] {
			fmt.Println(argument)
		}
		fmt.Println("--stdin--")
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "bounded":
		_, _ = io.WriteString(os.Stdout, "12345678")
		_, _ = io.WriteString(os.Stderr, "abcdefgh")
	case "exit":
		_, _ = io.WriteString(os.Stderr, args[2])
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "parent":
		startDescendant(args[1], "")
		for {
			time.Sleep(time.Hour)
		}
	case "overflow":
		startDescendant(args[1], args[2])
		for {
			_, _ = io.CopyN(os.Stdout, bytes.NewReader(bytes.Repeat([]byte("x"), 4096)), 4096)
		}
	case "descendant":
		signalIgnoreTERM()
		require.NoError(t, os.WriteFile(args[1], []byte(strconv.Itoa(os.Getpid())), 0o600))
		if args[2] == "" {
			for {
				time.Sleep(time.Hour)
			}
		}
		file, err := os.OpenFile(args[2], os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		require.NoError(t, err)
		defer file.Close()
		for {
			_, _ = file.Write([]byte("growing\n"))
			_ = file.Sync()
			time.Sleep(5 * time.Millisecond)
		}
	default:
		os.Exit(98)
	}
	os.Exit(0)
}

func startDescendant(pidFile, growthFile string) {
	command := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$", "--", "descendant", pidFile, growthFile)
	command.Env = []string{helperMarker + "=1"}
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		os.Exit(96)
	}
	for {
		if _, err := os.Stat(pidFile); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func signalIgnoreTERM() {
	signal.Ignore(syscall.SIGTERM)
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)
		return err == nil
	}, time.Second, 10*time.Millisecond)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	pid, err := strconv.Atoi(string(data))
	require.NoError(t, err)
	return pid
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool {
		err := syscall.Kill(pid, 0)
		return errors.Is(err, syscall.ESRCH)
	}, 2*time.Second, 10*time.Millisecond, "process %d is still alive", pid)
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Size()
}
