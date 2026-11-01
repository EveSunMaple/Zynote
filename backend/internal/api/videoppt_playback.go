package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
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

func (inv *Invoker) handleRetainedPlayback(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Limit json.RawMessage `json:"limit"`
	}
	if err := decodePlaybackInput(params, &input); err != nil {
		return nil, err
	}
	limit := 16
	if len(input.Limit) > 0 {
		if err := json.Unmarshal(input.Limit, &limit); err != nil || string(input.Limit) == "null" {
			return nil, contract.InputInvalid("留存列表数量无效")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := videoPptService(state).RetainedPlayback(ctx, limit)
	if err != nil {
		return nil, videoPptError(err)
	}
	return result, nil
}
func decodePlaybackInput(params json.RawMessage, target any) error {
	trimmed := bytes.TrimSpace(params)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return contract.InputInvalid("视频留存参数无效")
	}
	decoder := json.NewDecoder(bytes.NewReader(params))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return contract.InputInvalid("视频留存参数无效")
	}
	return nil
}
func (inv *Invoker) handlePreparePlaybackRelease(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input videoppt.PlaybackReleaseRequest
	if err := decodePlaybackInput(params, &input); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	result, err := videoPptService(state).PreparePlaybackRelease(ctx, input)
	if err != nil {
		var app *contract.AppError
		if errors.As(err, &app) {
			if data, ok := app.Data.(map[string]bool); ok && data["playbackReleaseNoMutation"] {
				return map[string]any{"status": "rejected", "mutation": "none", "code": app.Code, "message": app.Message}, nil
			}
		}
		return nil, videoPptError(err)
	}
	return result, nil
}
func (inv *Invoker) handleFinalizePlaybackRelease(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input videoppt.PlaybackReleaseRequest
	if err := decodePlaybackInput(params, &input); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	result, err := videoPptService(state).FinalizePlaybackRelease(ctx, input)
	if err != nil {
		return nil, videoPptError(err)
	}
	return result, nil
}
