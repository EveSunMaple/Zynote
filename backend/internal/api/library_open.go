package api

import (
	"encoding/json"
	"regexp"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
)

var libraryOpenExtension = regexp.MustCompile(`(?i)\.(pdf|pptx?|docx?|txt|md|srt)$`)

func (inv *Invoker) shellCall(store contract.StateStore, method string, params any) error {
	reverse := store.Reverse()
	if reverse == nil {
		return contract.Unavailable("系统文件操作不可用")
	}
	if _, err := reverse.Call(method, params, 15000); err != nil {
		return contract.Unavailable(err.Error())
	}
	return nil
}

func (inv *Invoker) handleLibraryOpen(store contract.StateStore, state *contract.State) (any, error) {
	if err := inv.shellCall(store, "shell.openPath", map[string]any{"path": state.LibraryRoot}); err != nil {
		return nil, contract.Unavailable("无法打开资料文件夹")
	}
	return state.LibraryRoot, nil
}

func (inv *Invoker) handleLibraryOpenFile(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Path == "" {
		return nil, contract.InputInvalid("文件路径无效")
	}
	path, err := library.ManagedFile(state.LibraryRoot, input.Path)
	if err != nil {
		return nil, err
	}
	if !libraryOpenExtension.MatchString(path) {
		return nil, contract.InputInvalid("仅支持打开 PDF、课件和文档")
	}
	if err := inv.shellCall(store, "shell.openPath", map[string]any{"path": path}); err != nil {
		return nil, contract.Unavailable("无法打开文件，请检查是否安装了 PDF 阅读器或 WPS")
	}
	return true, nil
}

func (inv *Invoker) handleLibraryReveal(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Path == "" {
		return nil, contract.InputInvalid("文件路径无效")
	}
	path, err := library.ManagedFile(state.LibraryRoot, input.Path)
	if err != nil {
		return nil, err
	}
	if err := inv.shellCall(store, "shell.showItemInFolder", map[string]any{"path": path}); err != nil {
		return nil, err
	}
	return true, nil
}

func (inv *Invoker) handleMediaOpen(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	value, err := inv.handleMediaFile(store, state, params)
	if err != nil {
		return nil, err
	}
	path, _ := value.(string)
	if path == "" {
		return nil, contract.Unavailable("下载成果路径无效")
	}
	if err := inv.shellCall(store, "shell.showItemInFolder", map[string]any{"path": path}); err != nil {
		return nil, err
	}
	return path, nil
}
