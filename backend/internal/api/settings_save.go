package api

import (
	"encoding/json"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

func (inv *Invoker) handleSettingsSave(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(params, &input); err != nil || len(input.Settings) == 0 {
		return nil, contract.InputInvalid("设置参数无效")
	}
	result, err := config.SaveSettings(
		state.DataRoot, state.PromptsRoot, store.Reverse(), input.Settings, inv.jobs.Current() != nil,
	)
	if err != nil {
		return nil, err
	}
	if result.ConnectionChanged && store.Reverse() != nil {
		model := result.Model
		if model == "" {
			model = "未配置"
		}
		if _, err := store.Reverse().Call(
			"tools.configure",
			map[string]any{"connection": result.Connection, "model": model},
			30000,
		); err != nil {
			return nil, contract.Unavailable("研究工具配置更新失败")
		}
	}
	return result.Settings, nil
}
