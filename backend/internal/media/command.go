package media

import (
	"context"
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

func streamMediaOutput(stdout io.Reader, stream func([]byte)) error {
	buffer := make([]byte, 65536)
	for {
		read, err := stdout.Read(buffer)
		if read > 0 {
			chunk := make([]byte, read)
			copy(chunk, buffer[:read])
			stream(chunk)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("媒体读取失败")
		}
	}
}

func readMediaOutput(stdout io.Reader, stream func([]byte)) ([]byte, error) {
	if stream != nil {
		return nil, streamMediaOutput(stdout, stream)
	}
	output, err := io.ReadAll(io.LimitReader(stdout, 2000000+1))
	if err != nil {
		return nil, err
	}
	if len(output) > 2000000 {
		return nil, fmt.Errorf("媒体检查响应过大")
	}
	return output, nil
}

func mediaWaitError(ctx context.Context, waitErr error) error {
	if ctx.Err() != nil {
		return contract.NewError(contract.CodeCancelled, "媒体处理已取消，恢复文件保留")
	}
	if waitErr != nil {
		return fmt.Errorf("媒体处理失败，未发布成果")
	}
	return nil
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
	output, readErr := readMediaOutput(stdout, input.Stream)
	if readErr != nil {
		_ = killMediaProcess(command)
		_ = command.Wait()
		return "", readErr
	}
	if err := mediaWaitError(ctx, command.Wait()); err != nil {
		return "", err
	}
	return string(output), nil
}
