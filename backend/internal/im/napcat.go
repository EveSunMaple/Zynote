package im

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

const (
	napcatVersion = "4.18.37"
	napcatSHA256  = "05c79761361321f6af4358aef92f80a7f8e319b6459c260ebf9a159615e96fe4"
)

type NapcatSecrets struct {
	WebUIPort   int    `json:"webui_port"`
	OneBotPort  int    `json:"onebot_port"`
	WebUIToken  string `json:"webui_token"`
	OneBotToken string `json:"onebot_token"`
}

type napcatJob interface {
	assign(process *os.Process) error
	terminate()
}

// NapcatRuntime owns the pinned NapCat component and its node.exe process tree.
type NapcatRuntime struct {
	mu         sync.Mutex
	startMu    sync.Mutex
	resources  string
	root       string
	crypt      Crypt
	state      string
	code       string
	value      *NapcatSecrets
	running    bool
	generation int
	child      *exec.Cmd
	job        napcatJob
	done       chan struct{}
	closed     bool
}

func NewNapcatRuntime(resources, root string, crypt Crypt) *NapcatRuntime {
	return &NapcatRuntime{resources: resources, root: root, crypt: crypt, state: "stopped"}
}

func (runtime *NapcatRuntime) archivePath() string {
	return filepath.Join(runtime.resources, "knowledge", "vendor", "NapCat.Shell.Windows.Node.zip")
}

func (runtime *NapcatRuntime) Availability() (bool, string) {
	if !napcatSupported() {
		return false, "内置 QQ 后台暂仅支持 Windows x64；手工连接已有 QQ 服务不受此限制。"
	}
	info, err := os.Stat(runtime.archivePath())
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return false, "QQ 组件未安装，请先获取 NapCat.Shell.Windows.Node.zip。"
	}
	return true, ""
}

func (runtime *NapcatRuntime) Status() map[string]any {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return map[string]any{
		"version": napcatVersion, "state": runtime.state, "code": runtime.code,
		"owned_running": runtime.running,
	}
}

func (runtime *NapcatRuntime) setState(state, code string) {
	runtime.mu.Lock()
	runtime.state = state
	runtime.code = code
	runtime.mu.Unlock()
}

func (runtime *NapcatRuntime) Close() {
	runtime.startMu.Lock()
	defer runtime.startMu.Unlock()
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return
	}
	runtime.closed = true
	runtime.running = false
	runtime.state = "stopped"
	runtime.mu.Unlock()
	runtime.stopProcess()
}

func (runtime *NapcatRuntime) currentValue() *NapcatSecrets {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.value == nil {
		return nil
	}
	value := *runtime.value
	return &value
}

func (runtime *NapcatRuntime) isRunning() bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.running && !runtime.closed
}

func (runtime *NapcatRuntime) childPID() int {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.child == nil || runtime.child.Process == nil {
		return 0
	}
	return runtime.child.Process.Pid
}

func (runtime *NapcatRuntime) markExited(generation int) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.generation != generation {
		return
	}
	runtime.running = false
	if !runtime.closed && runtime.state == "ready" {
		runtime.state = "failed"
		runtime.code = "native-exited"
	}
}

func (runtime *NapcatRuntime) ensure(ctx context.Context) (*NapcatSecrets, error) {
	runtime.startMu.Lock()
	defer runtime.startMu.Unlock()
	if value := runtime.currentValue(); value != nil && runtime.isRunning() {
		return value, nil
	}
	return runtime.start(ctx)
}
