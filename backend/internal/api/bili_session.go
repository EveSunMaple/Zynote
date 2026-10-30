package api

import (
	"context"
	"encoding/json"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

func (inv *Invoker) fetchVideoSeries(ctx context.Context, store contract.StateStore, state *contract.State, value string) (bilibili.VideoSeries, error) {
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return bilibili.VideoSeries{}, err
	}
	return resources.FetchSeries(ctx, value)
}
func (inv *Invoker) fetchVideoSubtitles(ctx context.Context, store contract.StateStore, state *contract.State, value string) (bilibili.FetchedSubtitle, error) {
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return bilibili.FetchedSubtitle{}, err
	}
	return resources.FetchSubtitles(ctx, value)
}
func (inv *Invoker) handleBiliOpen(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("网页地址无效")
	}
	if input.URL == "" {
		input.URL = "https://www.bilibili.com/"
	}
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	cookies, err := resources.BrowserCookies()
	if err != nil {
		return nil, err
	}
	if store.Reverse() == nil {
		return nil, contract.Unavailable("B站浏览窗口不可用")
	}
	_, err = store.Reverse().Call("shell.biliOpen", map[string]any{"url": input.URL, "cookies": cookies}, 30000)
	if err != nil {
		return nil, contract.Unavailable("B站浏览窗口无法打开，请重试")
	}
	return true, nil
}
