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
	"github.com/EveSunMaple/Zynote/backend/internal/jobs"
	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/media/download"
)

type handlerFunc func(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error)

type Invoker struct {
	handlers      map[string]handlerFunc
	native        *helper.Native
	youtube       *helper.Native
	embed         *helper.Native
	asr           *helper.Native
	media         *media.MediaService
	mediaRoot     string
	bili          *bilibili.Resources
	biliRoot      string
	download      *download.Service
	downloadRoot  string
	knowledge     *knowledge.Store
	knowledgeRoot string
	tokens        map[string]catalogToken
	tokenMu       sync.Mutex
	mediaMu       sync.Mutex
	notifier      contract.Notifier
	jobs          *jobs.Registry
}

func NewInvoker() *Invoker {
	return &Invoker{
		native: nil,
		jobs:   jobs.NewRegistry(),
		handlers: map[string]handlerFunc{
			"app:info":         requireState(handleAppInfo),
			"settings:get":     requireState(handleSettingsGet),
			"appearance:get":   requireState(handleAppearanceGet),
			"appearance:save":  requireState(handleAppearanceSave),
			"appearance:clear": requireState(handleAppearanceClear),
			"identity:get":     requireState(handleIdentityGet),
			"identity:save":    requireState(handleIdentitySave),
			"db:health":        requireState(handleDBHealth),
			"db:backup":        requireState(handleDBBackup),
			"course:detail":    requireState(handleCourseDetail),
			"library:list":     requireState(handleLibraryList),
			"course:create":    requireState(handleCourseCreate),
			"sessions":         requireState(handleSessions),
			"session:create":   requireState(handleSessionCreate),
			"db:bootstrap":     requireState(handleDBBootstrap),
			"course:delete":    requireState(handleCourseDelete),
			"session:delete":   requireState(handleSessionDelete),
			"lecture:delete":   requireState(handleLectureDelete),
			"lecture:detail":   requireState(handleLectureDetail),
		}}
}

func (inv *Invoker) SetNative(native *helper.Native) {
	inv.native = native
}

func (inv *Invoker) SetYoutubeNative(native *helper.Native) {
	inv.youtube = native
}

func (inv *Invoker) SetEmbedNative(native *helper.Native) {
	inv.embed = native
}

func (inv *Invoker) SetAsrNative(native *helper.Native) {
	inv.asr = native
}

func (inv *Invoker) SetNotifier(notifier contract.Notifier) {
	inv.notifier = notifier
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
		"bili-resources:logout", "bili-resources:folders", "bili-resources:videos", "media:catalog", "media:create", "media:discussion", "media:discussion-import":
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
		case "media:create":
			return inv.handleMediaCreate(store, state, params)
		case "media:discussion":
			return inv.handleMediaDiscussion(store, state, params)
		case "media:discussion-import":
			return inv.handleMediaDiscussionImport(store, state, params)
		default:
			return inv.handleBiliVideos(store, state, params)
		}
	case "notes:generate":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		return inv.handleNotesGenerate(store, state, params)
	case "notes:save":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		return inv.handleNotesSave(store, state, params)
	case "practice:submit", "practice:reset", "practice:answer", "practice:prepare", "practice:generate":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		switch action {
		case "practice:submit":
			return inv.handlePracticeSubmit(store, state, params)
		case "practice:reset":
			return inv.handlePracticeReset(store, state, params)
		case "practice:answer":
			return inv.handlePracticeAnswer(store, state, params)
		case "practice:prepare":
			return inv.handlePracticePrepare(store, state, params)
		default:
			return inv.handlePracticeGenerate(store, state, params)
		}
	case "questions:split":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		return inv.handleQuestionsSplit(store, state, params)
	case "subtitles:generate", "subtitles:save", "subtitles:import":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		switch action {
		case "subtitles:generate":
			return inv.handleSubtitlesGenerate(store, state, params)
		case "subtitles:save":
			return inv.handleSubtitlesSave(store, state, params)
		default:
			return inv.handleSubtitlesImport(store, state, params)
		}
	case "study:get", "study:save", "study:parse":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		switch action {
		case "study:get":
			return inv.handleStudyGet(store, state, params)
		case "study:save":
			return inv.handleStudySave(store, state, params)
		default:
			return inv.handleStudyParse(store, state, params)
		}
	case "library:delete":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		return inv.handleLibraryDelete(store, state, params)
	case "zhiyun:search", "zhiyun:lessons":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		if action == "zhiyun:search" {
			return inv.handleZhiyunSearch(store, state, params)
		}
		return inv.handleZhiyunLessons(store, state, params)
	case "settings:models", "settings:probe", "settings:test":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		switch action {
		case "settings:models":
			return inv.handleSettingsModels(store, state, params)
		case "settings:probe":
			return inv.handleSettingsProbe(store, state, params)
		default:
			return inv.handleSettingsTest(store, state, params)
		}
	case "chat", "cancel", "job:current":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		switch action {
		case "chat":
			return inv.handleChat(store, state, params)
		case "cancel":
			return inv.handleCancel(store, state, params)
		default:
			return inv.handleJobCurrent(store, state, params)
		}
	case "knowledge:ask", "knowledge:classification-suggest", "knowledge:vector-status",
		"knowledge:vector-enable", "knowledge:vector-rebuild", "knowledge:list", "knowledge:generation", "knowledge:read", "knowledge:import",
		"knowledge:citation", "knowledge:delete", "knowledge:organize", "knowledge:rebuild",
		"knowledge:search", "knowledge:classification-options", "knowledge:classification-read",
		"knowledge:classification-save":
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable("后台尚未完成握手")
		}
		switch action {
		case "knowledge:ask":
			return inv.handleKnowledgeAsk(store, state, params)
		case "knowledge:classification-suggest":
			return inv.handleKnowledgeClassificationSuggest(store, state, params)
		case "knowledge:list":
			return inv.handleKnowledgeList(store, state, params)
		case "knowledge:generation":
			return inv.handleKnowledgeGeneration(store, state, params)
		case "knowledge:read":
			return inv.handleKnowledgeRead(store, state, params)
		case "knowledge:import":
			return inv.handleKnowledgeImport(store, state, params)
		case "knowledge:citation":
			return inv.handleKnowledgeCitation(store, state, params)
		case "knowledge:delete":
			return inv.handleKnowledgeDelete(store, state, params)
		case "knowledge:organize":
			return inv.handleKnowledgeOrganize(store, state, params)
		case "knowledge:rebuild":
			return inv.handleKnowledgeRebuild(store, state, params)
		case "knowledge:search":
			return inv.handleKnowledgeSearch(store, state, params)
		case "knowledge:vector-status":
			return inv.handleKnowledgeVectorStatus(store, state, params)
		case "knowledge:vector-enable":
			return inv.handleKnowledgeVectorEnable(store, state, params)
		case "knowledge:vector-rebuild":
			return inv.handleKnowledgeVectorRebuild(store, state, params)
		case "knowledge:classification-options":
			return inv.handleKnowledgeClassificationOptions(store, state, params)
		case "knowledge:classification-read":
			return inv.handleKnowledgeClassificationRead(store, state, params)
		default:
			return inv.handleKnowledgeClassificationSave(store, state, params)
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
		Packaged:        input.Packaged,
		Capabilities:    contract.DefaultCapabilities,
	}
	if err := os.MkdirAll(state.DataRoot, 0o755); err != nil {
		return nil, contract.Unavailable("数据目录不可用")
	}
	if inv.native != nil {
		inv.native.Configure(input.NativePath, filepath.Join(input.ResourcesRoot, "pdfium", pdfiumFilename()), "", "", "")
	}
	if inv.youtube != nil {
		inv.youtube.Configure(input.NativePath, "", filepath.Join(state.DataRoot, "cache", "youtube"), "", "")
	}
	if inv.embed != nil {
		inv.embed.Configure(input.NativePath, "", "", filepath.Join(input.ResourcesRoot, "knowledge", "onnx-bge"), "")
	}
	if inv.asr != nil {
		inv.asr.Configure(input.NativePath, "", "", "", filepath.Join(input.ResourcesRoot, "asr", "onnx-small"))
	}
	store.SetState(state)
	slog.Info("hello accepted", "dataRoot", state.DataRoot)
	return state, nil
}

func pdfiumFilename() string {
	switch runtime.GOOS {
	case "windows":
		return "pdfium.dll"
	case "linux":
		return "libpdfium.so"
	default:
		return "libpdfium.dylib"
	}
}
