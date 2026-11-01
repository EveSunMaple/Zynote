package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"io"
	"time"
)

func (inv *Invoker) handleVideoPlaybackSource(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID                   string `json:"id"`
		ExpectedSourceSHA256 string `json:"expectedSourceSHA256"`
	}
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, contract.InputInvalid("视频播放参数无效")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := videoPptService(state).PlaybackSource(ctx, input.ID, input.ExpectedSourceSHA256)
	if err != nil {
		return nil, videoPptError(err)
	}
	return result, nil
}
