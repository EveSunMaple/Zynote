package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func textbookID(params json.RawMessage) (string, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return "", contract.InputInvalid("教材标识无效")
	}
	return input.ID, nil
}

func textbookDirectory(start string) string {
	current, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(current, "课本")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func (inv *Invoker) handleTextbookPick(store contract.StateStore, state *contract.State) (any, error) {
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("文件选择器不可用")
	}
	params := map[string]any{
		"title": "选择原教材 PDF", "extensions": []string{"pdf"},
	}
	if directory := textbookDirectory(state.AppRoot); directory != "" {
		params["defaultPath"] = directory
	}
	raw, err := reverse.Call("shell.pickFile", params, 600000)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	var picked struct {
		Path *string `json:"path"`
	}
	if json.Unmarshal(raw, &picked) != nil {
		return nil, contract.Unavailable("文件选择器响应无效")
	}
	if picked.Path == nil || *picked.Path == "" {
		return nil, nil
	}
	return inv.textbook.Select(inv.native, *picked.Path)
}

func (inv *Invoker) handleTextbookBytes(params json.RawMessage) (any, error) {
	id, err := textbookID(params)
	if err != nil {
		return nil, err
	}
	return inv.textbook.Bytes(id)
}

func (inv *Invoker) handleTextbookOpen(store contract.StateStore, params json.RawMessage) (any, error) {
	id, err := textbookID(params)
	if err != nil {
		return nil, err
	}
	path, err := inv.textbook.OpenPath(id)
	if err != nil {
		return nil, err
	}
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("系统文件打开不可用")
	}
	if _, err := reverse.Call("shell.openPath", map[string]any{"path": path}, 15000); err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	return true, nil
}

func (inv *Invoker) handleTextbookManual(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID        string          `json:"id"`
		Highlight json.RawMessage `json:"highlight"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) || len(input.Highlight) == 0 {
		return nil, contract.InputInvalid("划线数据无效")
	}
	return inv.runJob("手动划线", func(_ context.Context) (any, error) {
		return inv.textbook.Manual(inv.native, input.ID, input.Highlight)
	})
}

func (inv *Invoker) handleTextbookClear(state *contract.State, params json.RawMessage) (any, error) {
	id, err := textbookID(params)
	if err != nil {
		return nil, err
	}
	return inv.runJob("撤销划线", func(_ context.Context) (any, error) {
		return inv.textbook.Clear(inv.native, id)
	})
}

func (inv *Invoker) handleTextbookApply(state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID  string   `json:"id"`
		IDs []string `json:"ids"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("划线数据无效")
	}
	for _, id := range input.IDs {
		if !config.IsUUID(id) {
			return nil, contract.InputInvalid("请选择有效的划线")
		}
	}
	return inv.runJob("保存划线", func(_ context.Context) (any, error) {
		return inv.textbook.Apply(inv.native, input.ID, input.IDs)
	})
}
