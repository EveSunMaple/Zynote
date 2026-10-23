package api

import (
	"encoding/json"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
)

func (inv *Invoker) handleLibraryDelete(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Path      string `json:"path"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Path == "" {
		return nil, contract.InputInvalid("只能管理资料文件夹中的文件")
	}
	if inv.jobs.Current() != nil {
		return nil, contract.InputInvalid("请等待当前任务完成后再删除文件")
	}
	if !input.Confirmed {
		return nil, contract.InputInvalid("请先确认删除文件")
	}
	path, err := library.ManagedFile(state.LibraryRoot, input.Path)
	if err != nil {
		return nil, err
	}
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("文件删除服务不可用")
	}
	if _, err := reverse.Call("shell.trashItem", map[string]any{"path": path}, 30000); err != nil {
		return nil, contract.Unavailable("无法删除文件")
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	if err := write.DeleteManagedFile(path); err != nil {
		return nil, err
	}
	return true, nil
}
