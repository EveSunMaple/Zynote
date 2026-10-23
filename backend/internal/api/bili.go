package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

func (inv *Invoker) cryptAdapter(reverse contract.ReverseCaller) media.Crypt {
	return func(data []byte, decrypt bool) ([]byte, error) {
		if reverse == nil {
			return nil, fmt.Errorf("系统加密桥不可用")
		}
		method := "crypto.protect-bytes"
		if decrypt {
			method = "crypto.unprotect-bytes"
		}
		raw, err := reverse.Call(method, map[string]any{
			"value":  base64.StdEncoding.EncodeToString(data),
			"domain": "media",
		}, 5000)
		if err != nil {
			return nil, err
		}
		var reply struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(raw, &reply) != nil {
			return nil, fmt.Errorf("系统加密响应无效")
		}
		return base64.StdEncoding.DecodeString(reply.Value)
	}
}

func (inv *Invoker) biliResourcesFor(state *contract.State, reverse contract.ReverseCaller) (*bilibili.Resources, error) {
	root := filepath.Join(state.LibraryRoot, "MediaDownloads")
	inv.mediaMu.Lock()
	defer inv.mediaMu.Unlock()
	if inv.bili != nil && inv.biliRoot == root {
		return inv.bili, nil
	}
	crypt := inv.cryptAdapter(reverse)
	inv.bili = bilibili.NewResources(root, crypt)
	inv.biliRoot = root
	return inv.bili, nil
}

func (inv *Invoker) handleBiliStatus(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return resources.Status()
}

func (inv *Invoker) handleBiliBrowse(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Kind  string `json:"kind"`
		Query string `json:"query"`
		Page  *int   `json:"page"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("视频检索参数无效")
	}
	page := 1
	if input.Page != nil {
		page = *input.Page
	}
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return resources.Browse(input.Kind, input.Query, page)
}

func (inv *Invoker) handleMediaCatalog(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		URL      string `json:"url"`
		Playlist bool   `json:"playlist"`
		Options  any    `json:"options"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.URL == "" || len(input.URL) > 2000 {
		return nil, contract.InputInvalid("视频网址无效")
	}
	result, err := inv.readCatalogSource(state, store.Reverse(), input.URL, input.Playlist, input.Options)
	if err != nil {
		return nil, err
	}
	source, ok := result.(media.Source)
	if !ok {
		return result, nil
	}
	token := inv.storeCatalogToken(source)
	return catalogWithToken{Catalog: source.Catalog, Token: token}, nil
}

func (inv *Invoker) handleBiliQR(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	value, err := resources.Begin()
	if err != nil {
		return nil, err
	}
	return bilibili.QRImage(value)
}

func (inv *Invoker) handleBiliPoll(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return resources.Poll()
}

func (inv *Invoker) handleBiliLogout(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return resources.Logout()
}

func (inv *Invoker) handleBiliFolders(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return resources.Folders()
}

func (inv *Invoker) handleBiliVideos(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID   *float64 `json:"id"`
		Page *float64 `json:"page"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.ID == nil || input.Page == nil {
		return nil, contract.InputInvalid("收藏夹分页无效")
	}
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return resources.Videos(int64(*input.ID), int(*input.Page))
}
