package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type handlerFunc func(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error)

type Invoker struct {
	handlers map[string]handlerFunc
}

func NewInvoker() *Invoker {
	return &Invoker{handlers: map[string]handlerFunc{
		"core.hello":     handleHello,
		"app:info":       requireState(handleAppInfo),
		"settings:get":   requireState(handleSettingsGet),
		"appearance:get": requireState(handleAppearanceGet),
		"identity:get":   requireState(handleIdentityGet),
		"db:health":      requireState(handleDBHealth),
		"db:backup":      requireState(handleDBBackup),
		"course:detail":  requireState(handleCourseDetail),
		"library:list":   requireState(handleLibraryList),
	}}
}

func requireState(fn handlerFunc) handlerFunc {
	return func(store contract.StateStore, _ *contract.State, params json.RawMessage) (any, error) {
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		return fn(store, state, params)
	}
}

func (inv *Invoker) Invoke(store contract.StateStore, action string, params json.RawMessage) (any, error) {
	handler, ok := inv.handlers[action]
	if !ok {
		return nil, contract.InputInvalid("未知操作")
	}
	return handler(store, store.State(), params)
}

func isAbsPath(value string) bool {
	return value != "" && filepath.IsAbs(value)
}

func handleHello(store contract.StateStore, _ *contract.State, params json.RawMessage) (any, error) {
	var input contract.HelloParams
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("握手参数无效")
	}
	if input.Protocol != contract.ProtocolVersion {
		return nil, contract.Unavailable("后台协议版本不兼容，请完整更新应用")
	}
	if input.AppVersion == "" || input.ElectronVersion == "" {
		return nil, contract.InputInvalid("版本信息缺失")
	}
	if input.Platform != contract.CurrentPlatform() {
		return nil, contract.Unavailable("运行平台不匹配")
	}
	if !isAbsPath(input.DataRoot) || !isAbsPath(input.LibraryRoot) || !isAbsPath(input.ResourcesRoot) || !isAbsPath(input.PromptsRoot) {
		return nil, contract.InputInvalid("目录无效")
	}
	state := &contract.State{
		Protocol:        input.Protocol,
		Platform:        contract.CurrentPlatform(),
		PID:             os.Getpid(),
		GoVersion:       runtime.Version(),
		AppVersion:      input.AppVersion,
		ElectronVersion: input.ElectronVersion,
		DataRoot:        input.DataRoot,
		LibraryRoot:     input.LibraryRoot,
		ResourcesRoot:   input.ResourcesRoot,
		PromptsRoot:     input.PromptsRoot,
		Portable:        input.Portable,
		Capabilities:    contract.DefaultCapabilities,
	}
	if err := os.MkdirAll(state.DataRoot, 0o755); err != nil {
		return nil, contract.Unavailable("数据目录不可用")
	}
	store.SetState(state)
	slog.Info("hello accepted", "dataRoot", state.DataRoot)
	return state, nil
}
