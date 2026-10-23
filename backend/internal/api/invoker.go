package api

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/helper"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

type handlerFunc func(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error)

type Invoker struct {
	handlers  map[string]handlerFunc
	native    *helper.Native
	media     *media.MediaService
	mediaRoot string
	bili      *bilibili.Resources
	biliRoot  string
	mediaMu   sync.Mutex
}

func NewInvoker() *Invoker {
	return &Invoker{
		native: nil,
		handlers: map[string]handlerFunc{
			"app:info":       requireState(handleAppInfo),
			"settings:get":   requireState(handleSettingsGet),
			"appearance:get": requireState(handleAppearanceGet),
			"identity:get":   requireState(handleIdentityGet),
			"db:health":      requireState(handleDBHealth),
			"db:backup":      requireState(handleDBBackup),
			"course:detail":  requireState(handleCourseDetail),
			"library:list":   requireState(handleLibraryList),
			"course:create":  requireState(handleCourseCreate),
			"sessions":       requireState(handleSessions),
			"session:create": requireState(handleSessionCreate),
			"db:bootstrap":   requireState(handleDBBootstrap),
			"course:delete":  requireState(handleCourseDelete),
			"session:delete": requireState(handleSessionDelete),
			"lecture:delete": requireState(handleLectureDelete),
			"lecture:detail": requireState(handleLectureDetail),
		}}
}

func (inv *Invoker) SetNative(native *helper.Native) {
	inv.native = native
}

func requireState(fn handlerFunc) handlerFunc {
	return func(store contract.StateStore, _ *contract.State, params json.RawMessage) (any, error) {
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		return fn(store, state, params)
	}
}

func (inv *Invoker) Invoke(store contract.StateStore, action string, params json.RawMessage) (any, error) {
	switch action {
	case "core.hello":
		return inv.handleHello(store, params)
	case "pdf.text", "pdf.plainText", "pdf.inspect", "pdf.write":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		return inv.callNative(state, action, params)
	case "document:parse", "transcript:read":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		if action == "document:parse" {
			return inv.handleDocumentParse(state, params)
		}
		return inv.handleTranscriptRead(state, params)
	case "files:add":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		return inv.handleFilesAdd(state, params)
	case "files:remove":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		return inv.handleFilesRemove(state, params)
	case "bili-resources:status", "bili-resources:browse", "bili-resources:qr", "bili-resources:poll",
		"bili-resources:logout", "bili-resources:folders", "bili-resources:videos", "media:catalog":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		switch action {
		case "bili-resources:status":
			return inv.handleBiliStatus(store, state, params)
		case "bili-resources:browse":
			return inv.handleBiliBrowse(store, state, params)
		case "bili-resources:qr":
			return inv.handleBiliQR(store, state, params)
		case "bili-resources:poll":
			return inv.handleBiliPoll(store, state, params)
		case "bili-resources:logout":
			return inv.handleBiliLogout(store, state, params)
		case "bili-resources:folders":
			return inv.handleBiliFolders(store, state, params)
		case "media:catalog":
			return inv.handleMediaCatalog(store, state, params)
		default:
			return inv.handleBiliVideos(store, state, params)
		}
	case "media:options", "media:list", "media:retry", "media:cancel", "media:file",
		"media:auth-import", "media:auth-list", "media:auth-remove":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		switch action {
		case "media:options":
			return inv.handleMediaOptions(store, state, params)
		case "media:list":
			return inv.handleMediaList(store, state, params)
		case "media:retry":
			return inv.handleMediaRetry(store, state, params)
		case "media:cancel":
			return inv.handleMediaCancel(store, state, params)
		case "media:auth-import":
			return inv.handleMediaAuthImport(store, state, params)
		case "media:auth-list":
			return inv.handleMediaAuthList(store, state, params)
		case "media:auth-remove":
			return inv.handleMediaAuthRemove(store, state, params)
		default:
			return inv.handleMediaFile(store, state, params)
		}
	case "lecture:create", "lecture:transcript", "lecture:courseware", "questions:save":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		switch action {
		case "lecture:create":
			return inv.handleLectureCreate(store, state, params)
		case "lecture:transcript":
			return inv.handleLectureTranscript(store, state, params)
		case "lecture:courseware":
			return inv.handleLectureCourseware(store, state, params)
		default:
			return inv.handleQuestionsSave(store, state, params)
		}
	}
	handler, ok := inv.handlers[action]
	if !ok {
		return nil, contract.InputInvalid("未知操作")
	}
	return handler(store, store.State(), params)
}

func isAbsPath(value string) bool {
	return value != "" && filepath.IsAbs(value)
}

func (inv *Invoker) handleHello(store contract.StateStore, params json.RawMessage) (any, error) {
	var input contract.HelloParams
	if err := json.Unmarshal(params, &input); err != nil {
		return nil, contract.InputInvalid("握手参数无效")
	}
	if input.Protocol != contract.ProtocolVersion {
		return nil, contract.Unavailable("后台协议版本不兼容，请完整更新应用")
	}
	if input.AppVersion == "" || input.ElectronVersion == "" {
		return nil, contract.InputInvalid("版本信息缺失")
	}
	if input.Platform != contract.CurrentPlatform() {
		return nil, contract.Unavailable("运行平台不匹配")
	}
	if !isAbsPath(input.DataRoot) || !isAbsPath(input.LibraryRoot) || !isAbsPath(input.ResourcesRoot) || !isAbsPath(input.PromptsRoot) {
		return nil, contract.InputInvalid("目录无效")
	}
	if input.NativePath != "" && !isAbsPath(input.NativePath) {
		return nil, contract.InputInvalid("原生助手路径无效")
	}
	state := &contract.State{
		Protocol:        input.Protocol,
		Platform:        contract.CurrentPlatform(),
		PID:             os.Getpid(),
		GoVersion:       runtime.Version(),
		AppVersion:      input.AppVersion,
		ElectronVersion: input.ElectronVersion,
		DataRoot:        input.DataRoot,
		LibraryRoot:     input.LibraryRoot,
		ResourcesRoot:   input.ResourcesRoot,
		PromptsRoot:     input.PromptsRoot,
		Portable:        input.Portable,
		Capabilities:    contract.DefaultCapabilities,
	}
	if err := os.MkdirAll(state.DataRoot, 0o755); err != nil {
		return nil, contract.Unavailable("数据目录不可用")
	}
	if inv.native != nil {
		inv.native.Configure(input.NativePath, filepath.Join(input.ResourcesRoot, "pdfium", "libpdfium.dylib"))
	}
	store.SetState(state)
	slog.Info("hello accepted", "dataRoot", state.DataRoot)
	return state, nil
}
