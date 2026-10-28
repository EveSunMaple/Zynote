package api

import (
	"context"
	"encoding/json"

	"github.com/EveSunMaple/Zynote/backend/internal/ai"
	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type profileDraftInput struct {
	ID          string                            `json:"id"`
	Name        string                            `json:"name"`
	BaseURL     string                            `json:"baseUrl"`
	Model       string                            `json:"model"`
	VisionModel string                            `json:"visionModel"`
	Provider    string                            `json:"provider"`
	APIFormat   string                            `json:"apiFormat"`
	APIKey      string                            `json:"apiKey"`
	Parameters  map[string]config.ModelParameters `json:"parameters"`
}

func (input profileDraftInput) draft() config.ProfileDraft {
	return config.ProfileDraft{
		ID: input.ID, Name: input.Name, BaseURL: input.BaseURL, Model: input.Model,
		VisionModel: input.VisionModel, Provider: input.Provider, APIFormat: input.APIFormat,
		APIKey: input.APIKey, Parameters: input.Parameters,
	}
}

func (inv *Invoker) handleSettingsModels(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Profile *profileDraftInput `json:"profile"`
		Persist bool               `json:"persist"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Profile == nil {
		return nil, contract.InputInvalid("模型配置无效")
	}
	profile, err := config.DraftProfile(state.ConfigRoot, store.Reverse(), input.Profile.draft())
	if err != nil {
		return nil, err
	}
	result, err := inv.runJob("qwen-models", func(ctx context.Context) (any, error) {
		client := ai.New(ai.Profile{
			BaseURL: profile.BaseURL, Model: profile.Model, VisionModel: profile.VisionModel,
			APIFormat: profile.APIFormat, Key: profile.Key, Parameters: profile.Parameters,
		})
		models, err := client.Models(ctx)
		if err != nil {
			return nil, err
		}
		if input.Persist {
			if err := config.CacheModels(state.ConfigRoot, models); err != nil {
				return nil, err
			}
		}
		return models, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (inv *Invoker) handleSettingsProbe(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Profile *profileDraftInput `json:"profile"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Profile == nil {
		return nil, contract.InputInvalid("模型配置无效")
	}
	profile, err := config.DraftProfile(state.ConfigRoot, store.Reverse(), input.Profile.draft())
	if err != nil {
		return nil, err
	}
	message := "请只回复：连接成功。"
	return inv.runJob("test-qwen", func(ctx context.Context) (any, error) {
		client := ai.New(ai.Profile{
			BaseURL: profile.BaseURL, Model: profile.Model, VisionModel: profile.VisionModel,
			APIFormat: profile.APIFormat, Key: profile.Key, Parameters: profile.Parameters,
		})
		return client.Chat(ctx, []ai.Message{{Role: "user", Content: message}}, nil)
	})
}

func (inv *Invoker) handleSettingsTest(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	profile, err := config.ActiveProfile(state.ConfigRoot, store.Reverse())
	if err != nil {
		return nil, err
	}
	message := "请回复“连接成功”。"
	return inv.runJob("test-qwen", func(ctx context.Context) (any, error) {
		client := ai.New(ai.Profile{
			BaseURL: profile.BaseURL, Model: profile.Model, VisionModel: profile.VisionModel,
			APIFormat: profile.APIFormat, Key: profile.Key, Parameters: profile.Parameters,
		})
		return client.Chat(ctx, []ai.Message{{Role: "user", Content: message}}, nil)
	})
}
