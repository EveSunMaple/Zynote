package api

import (
	"context"
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
	"github.com/EveSunMaple/Zynote/backend/internal/media/platform"
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
	native, err := service.List()
	if err != nil {
		return nil, err
	}
	legacyRows, err := download.NewLegacyDownloads(service.Root).List()
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, row := range native {
		if id, ok := row["id"].(string); ok {
			ids[id] = true
		}
	}
	result := native
	for _, row := range legacyRows {
		if id, ok := row["id"].(string); ok && ids[id] {
			continue
		}
		result = append(result, row)
	}
	return result, nil
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
	if _, readErr := service.Jobs.Read(id); readErr == nil {
		downloadService, err := inv.downloadServiceFor(state, store.Reverse())
		if err != nil {
			return nil, err
		}
		if downloadService.Active(id) {
			return nil, contract.InputInvalid("该任务仍在执行")
		}
		updated, err := service.Retry(id)
		if err != nil {
			return nil, err
		}
		downloadService.Enqueue(id)
		return updated, nil
	}
	legacy := download.NewLegacyDownloads(service.Root)
	original, err := legacy.Read(id)
	if err != nil {
		return nil, err
	}
	if original.State == "completed" {
		if _, err := legacy.File(id); err != nil {
			return nil, err
		}
		rows, err := legacy.List()
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row["id"] == id {
				return row, nil
			}
		}
		return nil, contract.InputInvalid("旧任务尚无可确认成果")
	}
	recipe, err := legacy.Recipe(id)
	if err != nil {
		return nil, err
	}
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	freshResult, err := bilibili.NewCatalogReader(resources).Read(recipe.URL, false, recipe.Options)
	if err != nil {
		return nil, err
	}
	fresh, ok := freshResult.(media.Source)
	if !ok || fresh.Identity != recipe.Identity {
		return nil, contract.InputInvalid("旧任务与当前站点分集身份不一致，原记录保留")
	}
	if err := legacy.Unchanged(recipe.Row); err != nil {
		return nil, err
	}
	job, err := service.Jobs.Create(recipe.Identity, recipe.URL, recipe.Row.Title, recipe.Options, &media.JobLegacy{
		ID: id, RecordHash: recipe.Row.RecordHash,
	})
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
	if _, readErr := service.Jobs.Read(id); readErr == nil {
		return service.File(id)
	}
	return download.NewLegacyDownloads(service.Root).File(id)
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
	dispatcher := platform.NewDispatcher(bilibili.NewCatalogReader(resources))
	dispatcher.Youtube = inv.youtube
	runner := &download.Runner{
		Jobs:          service.Jobs,
		Auth:          service.Auth,
		Mux:           &media.NativeMediaMux{Resources: state.ResourcesRoot, AllowSystemFallback: allowSystem, Platform: contract.CurrentPlatform()},
		Catalog:       dispatcher,
		ScopedCatalog: dispatcher,
		Progress:      service.SetProgress,
	}
	inv.download = download.NewService(runner)
	inv.download.Finished = service.ClearProgress
	inv.downloadRoot = root
	return inv.download, nil
}

func existingMediaJob(service *media.MediaService, fresh media.Source, options media.NativeMediaOptions) (any, bool) {
	jobs, err := service.Jobs.List()
	if err != nil {
		return nil, false
	}
	for _, row := range jobs {
		if row.State != "completed" || row.Identity != fresh.Identity ||
			row.SourceURL != fresh.Catalog.URL ||
			!reflect.DeepEqual(row.Recipe, media.OptionsMap(options)) {
			continue
		}
		if _, fileErr := service.Jobs.File(row.ID); fileErr == nil {
			return service.Project(row), true
		}
	}
	return nil, false
}

func existingLegacyDownload(service *media.MediaService, fresh media.Source, options media.NativeMediaOptions) (any, bool, error) {
	legacy := download.NewLegacyDownloads(service.Root)
	legacyRows, err := legacy.List()
	if err != nil {
		return nil, false, err
	}
	for _, row := range legacyRows {
		if row["state"] != "completed" {
			continue
		}
		id, _ := row["id"].(string)
		recipe, readErr := legacy.Recipe(id)
		if readErr != nil || recipe.Identity != fresh.Identity ||
			!reflect.DeepEqual(media.OptionsMap(recipe.Options), media.OptionsMap(options)) {
			continue
		}
		if _, fileErr := legacy.File(id); fileErr == nil {
			return row, true, nil
		}
	}
	return nil, false, nil
}

func existingDownload(service *media.MediaService, fresh media.Source, options media.NativeMediaOptions) (any, bool, error) {
	if !options.SkipExisting {
		return nil, false, nil
	}
	if projection, ok := existingMediaJob(service, fresh, options); ok {
		return projection, true, nil
	}
	return existingLegacyDownload(service, fresh, options)
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
	freshResult, err := inv.readCatalogSource(state, store.Reverse(), saved.source.Catalog.URL, false, options)
	if err != nil {
		return nil, err
	}
	fresh, ok := freshResult.(media.Source)
	if !ok || fresh.Identity != saved.source.Identity {
		return nil, contract.InputInvalid("视频分集身份已改变，请重新读取")
	}
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	if projection, found, err := existingDownload(service, fresh, options); err != nil {
		return nil, err
	} else if found {
		return projection, nil
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

func (inv *Invoker) readCatalogSource(state *contract.State, reverse contract.ReverseCaller, input string, playlist bool, options any) (any, error) {
	selection, err := platform.Input(input)
	if err != nil {
		return nil, contract.InputInvalid(err.Error())
	}
	settings, err := media.MediaOptions(options)
	if err != nil {
		return nil, err
	}
	if selection.Platform == "bilibili" && settings.CookieProfile == "" {
		resources, err := inv.biliResourcesFor(state, reverse)
		if err != nil {
			return nil, err
		}
		return bilibili.NewCatalogReader(resources).Read(input, playlist, settings)
	}
	if settings.CookieProfile == "" && settings.UseCookies {
		return nil, contract.InputInvalid("此平台需明确选择导入的登录档案；公开下载可关闭使用登录档案")
	}
	service, err := inv.mediaServiceFor(state, reverse)
	if err != nil {
		return nil, err
	}
	transport, err := media.NewMediaTransport(settings)
	if err != nil {
		return nil, err
	}
	defer transport.Close()
	scoped, err := service.Auth.Scoped(selection.URL, settings, media.AuthFetcher(transport.Fetch))
	if err != nil {
		return nil, err
	}
	if selection.Platform == "bilibili" {
		return bilibili.NewCatalogReader(bilibili.NewScopedResources(bilibili.ScopedFetcher(scoped))).Read(input, playlist, settings)
	}
	if selection.Platform == "youtube" {
		cookie := ""
		if settings.UseCookies && settings.CookieProfile != "" {
			value, err := service.Auth.CookieHeader(settings.CookieProfile, "youtube")
			if err != nil {
				return nil, err
			}
			cookie = value
		}
		if _, err := inv.youtube.Call("youtube.auth", map[string]any{"cookie": cookie}, 30000); err != nil {
			return nil, contract.Unavailable(err.Error())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return platform.Read(ctx, selection, playlist, settings, scoped, inv.youtube)
}

func (inv *Invoker) handleMediaDiscussion(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		URL   string `json:"url"`
		Pages int    `json:"pages"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("讨论采集参数无效")
	}
	if input.Pages == 0 {
		input.Pages = 2
	}
	if input.Pages < 1 || input.Pages > 10 {
		return nil, contract.InputInvalid("讨论采集页数无效")
	}
	resources, err := inv.biliResourcesFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	service, err := inv.mediaServiceFor(state, store.Reverse())
	if err != nil {
		return nil, err
	}
	discussionStore, err := bilibili.NewDiscussionStore(service.Root)
	if err != nil {
		return nil, err
	}
	return bilibili.NewDiscussion(resources, discussionStore).Collect(input.URL, input.Pages)
}
