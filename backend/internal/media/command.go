package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type MediaCommandInput struct {
	Resources           string
	Tool                string
	Args                []string
	Ctx                 context.Context // NOSONAR: request-scoped input struct; the caller owns this context for a single operation.
	Stream              func([]byte)
	OnStarted           func(pid int)
	AllowSystemFallback bool
	Platform            string
}

func MediaCommand(input MediaCommandInput) (string, error) {
	ctx := input.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return "", contract.NewError(contract.CodeCancelled, "媒体处理已取消，恢复文件保留")
	}
	platform := input.Platform
	if platform == "" {
		platform = CurrentMediaPlatform()
	}
	if _, err := MediaExecutablePath(input.Resources, input.Tool, platform); err != nil {
		return "", err
	}
	executable, err := CheckedMediaExecutable(input.Resources, input.Tool, platform, input.AllowSystemFallback)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, executable, input.Args...)
	configureMediaProcess(command)
	command.Cancel = func() error { return killMediaProcess(command) }
	command.WaitDelay = 5 * time.Second
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("媒体守护进程无法启动")
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("媒体守护进程无法启动")
	}
	if input.OnStarted != nil {
		input.OnStarted(command.Process.Pid)
	}
	var output []byte
	readErr := error(nil)
	if input.Stream != nil {
		buffer := make([]byte, 65536)
		for {
			read, err := stdout.Read(buffer)
			if read > 0 {
				chunk := make([]byte, read)
				copy(chunk, buffer[:read])
				input.Stream(chunk)
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				readErr = fmt.Errorf("媒体读取失败")
				break
			}
		}
	} else {
		limited := io.LimitReader(stdout, 2000000+1)
		output, readErr = io.ReadAll(limited)
		if readErr == nil && len(output) > 2000000 {
			readErr = fmt.Errorf("媒体检查响应过大")
		}
	}
	if readErr != nil {
		_ = killMediaProcess(command)
		_ = command.Wait()
		return "", readErr
	}
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return "", contract.NewError(contract.CodeCancelled, "媒体处理已取消，恢复文件保留")
	}
	if waitErr != nil {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			return "", fmt.Errorf("媒体处理失败，未发布成果")
		}
		return "", fmt.Errorf("媒体处理失败，未发布成果")
	}
	return string(output), nil
}
