//go:build windows

package media

import "os/exec"

func configureMediaProcess(command *exec.Cmd) {} // NOSONAR: empty function is an intentional interface implementation.

func killMediaProcess(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}
