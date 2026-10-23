package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
)

var mediaJobIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (inv *Invoker) mediaServiceFor(state *contract.State, reverse contract.ReverseCaller) (*media.MediaService, error) {
	root := filepath.Join(state.LibraryRoot, "MediaDownloads")
	inv.mediaMu.Lock()
	defer inv.mediaMu.Unlock()
	if inv.media != nil && inv.mediaRoot == root {
		return inv.media, nil
	}
	crypt := func(data []byte, decrypt bool) ([]byte, error) {
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
	inv.media = media.NewMediaService(root, crypt)
	inv.mediaRoot = root
	return inv.media, nil
}

func (inv *Invoker) handleMediaOptions(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return service.Options()
}

func (inv *Invoker) handleMediaList(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return service.List()
}

func (inv *Invoker) handleMediaRetry(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	id, err := mediaJobID(params)
	if err != nil {
		return nil, err
	}
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return service.Retry(id)
}

func (inv *Invoker) handleMediaCancel(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	id, err := mediaJobID(params)
	if err != nil {
		return nil, err
	}
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return service.Cancel(id)
}

func (inv *Invoker) handleMediaFile(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	id, err := mediaJobID(params)
	if err != nil {
		return nil, err
	}
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return service.File(id)
}

func mediaJobID(params json.RawMessage) (string, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !mediaJobIDPattern.MatchString(input.ID) {
		return "", contract.InputInvalid("下载任务标识无效")
	}
	return input.ID, nil
}
