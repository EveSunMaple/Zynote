package api

import (
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
