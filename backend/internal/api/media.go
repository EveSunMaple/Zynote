package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/media/download"
)

var mediaJobIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (inv *Invoker) mediaServiceFor(state *contract.State, reverse contract.ReverseCaller) (*media.MediaService, error) {
	root := filepath.Join(state.LibraryRoot, "MediaDownloads")
	inv.mediaMu.Lock()
	defer inv.mediaMu.Unlock()
	if inv.media != nil && inv.mediaRoot == root {
		return inv.media, nil
	}
	inv.media = media.NewMediaService(root, inv.cryptAdapter(reverse))
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
	if inv.download != nil && inv.download.Cancel(id) {
		for attempt := 0; attempt < 50; attempt++ {
			row, readErr := service.Jobs.Read(id)
			if readErr != nil || row.State != "running" {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
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

type catalogToken struct {
	source  bilibili.Source
	expires time.Time
}

type catalogWithToken struct {
	bilibili.Catalog
	Token string `json:"token"`
}

func (inv *Invoker) storeCatalogToken(source bilibili.Source) string {
	token := strings.ReplaceAll(ids.New(), "-", "")
	inv.tokenMu.Lock()
	defer inv.tokenMu.Unlock()
	if inv.tokens == nil {
		inv.tokens = map[string]catalogToken{}
	}
	now := time.Now()
	for key, item := range inv.tokens {
		if item.expires.Before(now) {
			delete(inv.tokens, key)
		}
	}
	if len(inv.tokens) >= 200 {
		for key := range inv.tokens {
			delete(inv.tokens, key)
			break
		}
	}
	inv.tokens[token] = catalogToken{source: source, expires: now.Add(15 * time.Minute)}
	return token
}

func (inv *Invoker) downloadServiceFor(state *contract.State, reverse contract.ReverseCaller) (*download.Service, error) {
	resources, err := inv.biliResourcesFor(state, reverse)
	if err != nil {
		return nil, err
	}
	service, err := inv.mediaServiceFor(state, reverse)
	if err != nil {
		return nil, err
	}
	root := service.Root
	inv.mediaMu.Lock()
	defer inv.mediaMu.Unlock()
	if inv.download != nil && inv.downloadRoot == root {
		return inv.download, nil
	}
	allowSystem := !state.Packaged || os.Getenv("ZYNOTE_SYSTEM_MEDIA") == "1"
	runner := &download.Runner{
		Jobs:     service.Jobs,
		Auth:     service.Auth,
		Mux:      &media.NativeMediaMux{Resources: state.ResourcesRoot, AllowSystemFallback: allowSystem, Platform: contract.CurrentPlatform()},
		Catalog:  bilibili.NewCatalogReader(resources),
		Progress: service.SetProgress,
	}
	inv.download = download.NewService(runner)
	inv.download.Finished = service.ClearProgress
	inv.downloadRoot = root
	return inv.download, nil
}

func (inv *Invoker) handleMediaCreate(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Token   string `json:"token"`
		Options any    `json:"options"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !mediaJobIDPattern.MatchString(input.Token) {
		return nil, contract.InputInvalid("格式目录已失效，请重新读取")
	}
	inv.tokenMu.Lock()
	saved, ok := inv.tokens[input.Token]
	inv.tokenMu.Unlock()
	if !ok || saved.expires.Before(time.Now()) {
		return nil, contract.InputInvalid("格式目录已失效，请重新读取")
	}
	options, err := media.MediaOptions(input.Options)
	if err != nil {
		return nil, err
	}
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	freshResult, err := bilibili.NewCatalogReader(resources).Read(saved.source.Catalog.URL, false, options)
	if err != nil {
		return nil, err
	}
	fresh, ok := freshResult.(bilibili.Source)
	if !ok || fresh.Identity != saved.source.Identity {
		return nil, contract.InputInvalid("视频分集身份已改变，请重新读取")
	}
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	if options.SkipExisting {
		jobs, err := service.Jobs.List()
		if err == nil {
			for _, row := range jobs {
				if row.State != "completed" || row.Identity != fresh.Identity ||
					row.SourceURL != fresh.Catalog.URL ||
					!reflect.DeepEqual(row.Recipe, media.OptionsMap(options)) {
					continue
				}
				if _, fileErr := service.Jobs.File(row.ID); fileErr == nil {
					return service.Project(row), nil
				}
			}
		}
	}
	job, err := service.Jobs.Create(fresh.Identity, fresh.Catalog.URL, fresh.Catalog.Title, options, nil)
	if err != nil {
		return nil, err
	}
	downloadService, err := inv.downloadServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	downloadService.Enqueue(job.ID)
	return service.Project(job), nil
}
