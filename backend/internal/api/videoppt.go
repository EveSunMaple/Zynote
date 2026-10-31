package api

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
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
		return inv.fetchVideoSeries(ctx, store, state, input.URL)
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
		return inv.fetchVideoSubtitles(ctx, store, state, input.URL)
	})
}

func (inv *Invoker) handleVideoPptReveal(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(params, &input) != nil {
		return nil, contract.InputInvalid("任务编号无效")
	}
	directory, err := videoPptService(state).Directory(input.ID)
	if err != nil {
		return nil, err
	}
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("文件管理器不可用")
	}
	if _, err := reverse.Call("shell.openPath", map[string]any{"path": directory}, 30000); err != nil {
		return nil, contract.Unavailable("任务文件夹无法打开")
	}
	return true, nil
}
