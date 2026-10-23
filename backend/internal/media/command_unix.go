//go:build !windows

package media

import (
	"os/exec"
	"syscall"
)

func configureMediaProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killMediaProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
		return command.Process.Kill()
	}
	return nil
}
