package api

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	mediaBilibili "github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

func videoPptService(state *contract.State) *videoppt.Service {
	return videoppt.New(filepath.Join(state.LibraryRoot, "视频提取临时任务"))
}

func videoPptError(err error) error {
	var request *videoppt.RequestError
	if errors.As(err, &request) {
		return contract.InputInvalid(request.Message)
	}
	return contract.Unavailable(err.Error())
}

func (inv *Invoker) handleVideoPptPending(state *contract.State) (any, error) {
	result, err := videoPptService(state).Pending()
	if err != nil {
		return nil, videoPptError(err)
	}
	return result, nil
}

func (inv *Invoker) handleVideoPptRestore(state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.ID == "" {
		return nil, contract.InputInvalid("任务编号无效")
	}
	result, err := videoPptService(state).Restore(input.ID)
	if err != nil {
		return nil, videoPptError(err)
	}
	return result, nil
}

func (inv *Invoker) handleVideoPptSeries(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.URL == "" {
		return nil, contract.InputInvalid("视频网址无效")
	}
	return inv.runJob("video-directory", func(ctx context.Context) (any, error) {
		return mediaBilibili.FetchVideoSeries(ctx, nil, input.URL)
	})
}

func (inv *Invoker) handleVideoPptSubtitle(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.URL == "" {
		return nil, contract.InputInvalid("视频网址无效")
	}
	return inv.runJob("video-subtitle", func(ctx context.Context) (any, error) {
		return mediaBilibili.FetchVideoSubtitles(ctx, nil, input.URL)
	})
}
