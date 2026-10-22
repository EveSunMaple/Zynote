package api

import (
	"encoding/json"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

func handleAppInfo(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return map[string]any{
		"version":  state.AppVersion,
		"electron": state.ElectronVersion,
		"platform": state.Platform,
	}, nil
}

func handleSettingsGet(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return config.ReadSettings(state.DataRoot, state.PromptsRoot, store.Reverse())
}

func handleAppearanceGet(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return config.ReadAppearance(state.DataRoot)
}

func handleIdentityGet(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return config.ReadIdentity(state.DataRoot)
}

func handleDBHealth(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return storage.Health(filepath.Join(state.DataRoot, "assistant.db"))
}

func handleDBBackup(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Target string `json:"target"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, contract.InputInvalid("备份参数无效")
		}
	}
	return storage.Backup(filepath.Join(state.DataRoot, "assistant.db"), input.Target)
}
