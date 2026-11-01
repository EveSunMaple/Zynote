//go:build !windows || !amd64

package im

import (
	"context"
	"fmt"
)

func napcatSupported() bool { return false }

func (runtime *NapcatRuntime) start(context.Context) (*NapcatSecrets, error) {
	return nil, fmt.Errorf("内置 QQ 后台暂仅支持 Windows x64")
}

func (runtime *NapcatRuntime) stopProcess() {
	// 非 Windows x64 平台没有可停止的内置 QQ 后台进程。
}
