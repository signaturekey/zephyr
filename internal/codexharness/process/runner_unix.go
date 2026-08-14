//go:build unix

package process

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

const terminationGrace = 100 * time.Millisecond

func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func terminateProcessGroup(pid int) error {
	termErr := syscall.Kill(-pid, syscall.SIGTERM)
	if termErr != nil && !errors.Is(termErr, syscall.ESRCH) {
		return termErr
	}
	time.Sleep(terminationGrace)
	killErr := syscall.Kill(-pid, syscall.SIGKILL)
	if killErr != nil && !errors.Is(killErr, syscall.ESRCH) {
		return killErr
	}
	return nil
}
