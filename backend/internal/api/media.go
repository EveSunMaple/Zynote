package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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

func (inv *Invoker) handleMediaAuthList(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return service.Auth.List()
}

func (inv *Invoker) handleMediaAuthImport(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Name string `json:"name"`
		Site string `json:"site"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("登录档案参数无效")
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 80 {
		return nil, contract.InputInvalid("请填写档案名称")
	}
	if len(input.Site) < 1 || len(input.Site) > 253 {
		return nil, contract.InputInvalid("站点域名无效")
	}
	reverse := store.Reverse()
	if reverse == nil {
		return nil, contract.Unavailable("文件选择器不可用")
	}
	raw, err := reverse.Call("shell.pickFile", map[string]any{
		"title":      "明确选择要导入的站点 cookies.txt",
		"extensions": []string{"txt", "cookies"},
	}, 600000)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	var picked struct {
		Path *string `json:"path"`
	}
	if json.Unmarshal(raw, &picked) != nil {
		return nil, contract.Unavailable("文件选择器响应无效")
	}
	if picked.Path == nil || *picked.Path == "" {
		return nil, nil
	}
	info, err := os.Lstat(*picked.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 1_048_576 {
		return nil, contract.InputInvalid("登录档案须为不超过1MB的普通文本文件")
	}
	cookies, err := os.ReadFile(*picked.Path)
	if err != nil {
		return nil, contract.InputInvalid("登录档案须为不超过1MB的普通文本文件")
	}
	service, err := inv.mediaServiceFor(state, reverse)
	if err != nil {
		return nil, err
	}
	return service.Auth.Save(media.SaveProfileInput{
		Name: input.Name, Site: input.Site, Cookies: string(cookies),
	})
}

func (inv *Invoker) handleMediaAuthRemove(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID        string `json:"id"`
		Confirmed bool   `json:"confirmed"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !mediaJobIDPattern.MatchString(input.ID) {
		return nil, contract.InputInvalid("登录档案标识无效")
	}
	if !input.Confirmed {
		return nil, contract.InputInvalid("请先确认删除登录档案")
	}
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	return service.Auth.Remove(input.ID)
}
