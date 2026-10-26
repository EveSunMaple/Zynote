package api

import (
	"encoding/json"
	"fmt"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	runtimecomponents "github.com/EveSunMaple/Zynote/backend/internal/runtime"
)

func handleRuntimeStatus(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return runtimecomponents.CheckStatus(state.ResourcesRoot, state.AppVersion), nil
}

func handleRuntimeInstall(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(params, &input) != nil || input.ID == "" {
		return nil, contract.InputInvalid("组件标识无效")
	}
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("系统文件操作不可用")
	}
	raw, err := reverse.Call("shell.pickDirectory", map[string]any{
		"title": "选择同版本组件文件夹", "defaultPath": state.ResourcesRoot,
	}, 120000)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	var picked struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &picked) != nil {
		return nil, contract.Unavailable("组件文件夹选择结果无效")
	}
	if picked.Path == "" {
		return nil, nil
	}
	result, err := runtimecomponents.Install(state.ResourcesRoot, state.AppVersion, input.ID, picked.Path)
	if err != nil {
		return nil, contract.InputInvalid(fmt.Sprintf("%v", err))
	}
	return result, nil
}
