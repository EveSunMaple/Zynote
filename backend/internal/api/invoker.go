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
	"github.com/EveSunMaple/Zynote/backend/internal/im"
	"github.com/EveSunMaple/Zynote/backend/internal/jobs"
	"github.com/EveSunMaple/Zynote/backend/internal/knowledge"
	"github.com/EveSunMaple/Zynote/backend/internal/media"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
	"github.com/EveSunMaple/Zynote/backend/internal/media/download"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
	"github.com/EveSunMaple/Zynote/backend/internal/textbook"
)

const msgHandshakePending = "后台尚未完成握手"

type handlerFunc func(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error)

type Invoker struct {
	handlers      map[string]handlerFunc
	actions       map[string]actionFunc
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
	textbook      *textbook.Service
	tokens        map[string]catalogToken
	tokenMu       sync.Mutex
	mediaMu       sync.Mutex
	notifier      contract.Notifier
	jobs          *jobs.Registry
	im            *im.Service
	imRoot        string
	imMu          sync.Mutex
}

func NewInvoker() *Invoker {
	inv := &Invoker{
		native:   nil,
		jobs:     jobs.NewRegistry(),
		textbook: textbook.New(),
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
			"runtime:status":   requireState(handleRuntimeStatus),
			"runtime:install":  requireState(handleRuntimeInstall),
		}}
	inv.actions = registerActions(inv)
	return inv
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
			return nil, contract.Unavailable(msgHandshakePending)
		}
		return fn(store, state, params)
	}
}

type actionFunc func(store contract.StateStore, action string, params json.RawMessage) (any, error)

func actionWithState(fn func(contract.StateStore, *contract.State, json.RawMessage) (any, error)) actionFunc {
	return func(store contract.StateStore, _ string, params json.RawMessage) (any, error) {
		state := store.State()
		if state == nil {
			return nil, contract.Unavailable(msgHandshakePending)
		}
		return fn(store, state, params)
	}
}

func actionStateParams(fn func(*contract.State, json.RawMessage) (any, error)) func(contract.StateStore, *contract.State, json.RawMessage) (any, error) {
	return func(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
		return fn(state, params)
	}
}

func actionStateOnly(fn func(*contract.State) (any, error)) func(contract.StateStore, *contract.State, json.RawMessage) (any, error) {
	return func(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
		return fn(state)
	}
}

func actionStoreState(fn func(contract.StateStore, *contract.State) (any, error)) func(contract.StateStore, *contract.State, json.RawMessage) (any, error) {
	return func(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
		return fn(store, state)
	}
}

func actionStoreParams(fn func(contract.StateStore, json.RawMessage) (any, error)) func(contract.StateStore, *contract.State, json.RawMessage) (any, error) {
	return func(store contract.StateStore, _ *contract.State, params json.RawMessage) (any, error) {
		return fn(store, params)
	}
}

func actionParamsOnly(fn func(json.RawMessage) (any, error)) func(contract.StateStore, *contract.State, json.RawMessage) (any, error) {
	return func(_ contract.StateStore, _ *contract.State, params json.RawMessage) (any, error) {
		return fn(params)
	}
}

func registerActions(inv *Invoker) map[string]actionFunc {
	return map[string]actionFunc{
		"core.hello": func(store contract.StateStore, _ string, params json.RawMessage) (any, error) {
			return inv.handleHello(store, params)
		},
		"im:draft": actionWithState(inv.handleImDraft),
		"pdf.text": func(store contract.StateStore, action string, params json.RawMessage) (any, error) {
			return inv.invokeNative(store, action, params)
		},
		"pdf.plainText": func(store contract.StateStore, action string, params json.RawMessage) (any, error) {
			return inv.invokeNative(store, action, params)
		},
		"pdf.inspect": func(store contract.StateStore, action string, params json.RawMessage) (any, error) {
			return inv.invokeNative(store, action, params)
		},
		"pdf.write": func(store contract.StateStore, action string, params json.RawMessage) (any, error) {
			return inv.invokeNative(store, action, params)
		},
		"document:parse":                      actionWithState(actionStateParams(inv.handleDocumentParse)),
		"transcript:read":                     actionWithState(actionStateParams(inv.handleTranscriptRead)),
		"files:add":                           actionWithState(actionStateParams(inv.handleFilesAdd)),
		"files:remove":                        actionWithState(actionStateParams(inv.handleFilesRemove)),
		"bili-resources:status":               actionWithState(inv.handleBiliStatus),
		"bili-resources:browse":               actionWithState(inv.handleBiliBrowse),
		"bili-resources:qr":                   actionWithState(inv.handleBiliQR),
		"bili-resources:poll":                 actionWithState(inv.handleBiliPoll),
		"bili-resources:logout":               actionWithState(inv.handleBiliLogout),
		"bili-resources:folders":              actionWithState(inv.handleBiliFolders),
		"bili-resources:videos":               actionWithState(inv.handleBiliVideos),
		"media:catalog":                       actionWithState(inv.handleMediaCatalog),
		"media:create":                        actionWithState(inv.handleMediaCreate),
		"media:discussion":                    actionWithState(inv.handleMediaDiscussion),
		"media:discussion-import":             actionWithState(inv.handleMediaDiscussionImport),
		"notes:generate":                      actionWithState(inv.handleNotesGenerate),
		"notes:save":                          actionWithState(inv.handleNotesSave),
		"practice:submit":                     actionWithState(inv.handlePracticeSubmit),
		"practice:reset":                      actionWithState(inv.handlePracticeReset),
		"practice:answer":                     actionWithState(inv.handlePracticeAnswer),
		"practice:prepare":                    actionWithState(inv.handlePracticePrepare),
		"practice:generate":                   actionWithState(inv.handlePracticeGenerate),
		"questions:split":                     actionWithState(inv.handleQuestionsSplit),
		"subtitles:generate":                  actionWithState(inv.handleSubtitlesGenerate),
		"subtitles:save":                      actionWithState(inv.handleSubtitlesSave),
		"subtitles:import":                    actionWithState(inv.handleSubtitlesImport),
		"study:get":                           actionWithState(inv.handleStudyGet),
		"study:save":                          actionWithState(inv.handleStudySave),
		"study:parse":                         actionWithState(inv.handleStudyParse),
		"subtitle-derived:list":               actionWithState(actionStateOnly(inv.handleSubtitleDerivedList)),
		"subtitle-derived:resume":             actionWithState(inv.handleSubtitleDerivedResume),
		"subtitle-derived:start":              actionWithState(inv.handleSubtitleDerivedStart),
		"subtitle-sync:list":                  actionWithState(actionStateOnly(inv.handleSubtitleSyncList)),
		"subtitle-sync:start":                 actionWithState(inv.handleSubtitleSyncStart),
		"subtitle-sync:mixed":                 actionWithState(inv.handleSubtitleSyncMixed),
		"subtitle-sync:resume":                actionWithState(inv.handleSubtitleSyncResume),
		"subtitle-sync:attach":                actionWithState(inv.handleSubtitleSyncAttach),
		"video-ppt:pending":                   actionWithState(actionStateOnly(inv.handleVideoPptPending)),
		"video-ppt:restore":                   actionWithState(actionStateParams(inv.handleVideoPptRestore)),
		"video-ppt:reveal":                    actionWithState(inv.handleVideoPptReveal),
		"video-ppt:subtitle-import":           actionWithState(inv.handleVideoSubtitleImport),
		"video-ppt:subtitle-save":             actionWithState(actionStateParams(inv.handleVideoSubtitleSave)),
		"video-ppt:subtitle-export":           actionWithState(actionStateParams(inv.handleVideoSubtitleExport)),
		"video-ppt:series":                    actionWithState(inv.handleVideoPptSeries),
		"video-ppt:subtitle":                  actionWithState(inv.handleVideoPptSubtitle),
		"video-ppt:subtitle-transcribe-local": actionWithState(inv.handleVideoSubtitleTranscribeLocal),
		"video-ppt:resume-subtitle":           actionWithState(actionStateParams(inv.handleVideoResumeSubtitle)),
		"video-ppt:source":                    actionWithState(inv.handleVideoPptSource),
		"video-ppt:extract":                   actionWithState(inv.handleVideoPptExtract),
		"video-ppt:export":                    actionWithState(inv.handleVideoPptExport),
		"video-ppt:auto":                      actionWithState(inv.handleVideoPptAuto),
		"video-ppt:summary":                   actionWithState(inv.handleVideoSummary),
		"video-ppt:notes-export":              actionWithState(inv.handleVideoNotesExport),
		"video-ppt:batch":                     actionWithState(inv.handleVideoBatch),
		"video-ppt:batch-subtitles":           actionWithState(inv.handleVideoBatchSubtitles),
		"video-ppt:subtitle-transcribe":       actionWithState(inv.handleVideoSubtitleTranscribe),
		"ppt-extract:analyze":                 actionWithState(inv.handlePptExtractAnalyze),
		"ppt-extract:export":                  actionWithState(inv.handlePptExtractExport),
		"visual:list":                         actionWithState(actionStateOnly(inv.handleVisualList)),
		"visual:prepare":                      actionWithState(inv.handleVisualPrepare),
		"visual:prepare-resume":               actionWithState(actionStateParams(inv.handleVisualPrepareResume)),
		"visual:recognize":                    actionWithState(inv.handleVisualRecognize),
		"visual:recover-output":               actionWithState(actionStateParams(inv.handleVisualRecoverOutput)),
		"tools:request":                       actionWithState(inv.handleToolsRequest),
		"tools:skill-open":                    actionWithState(actionStoreState(inv.handleSkillOpen)),
		"export":                              actionWithState(inv.handleExport),
		"courses":                             actionWithState(actionStateOnly(inv.handleCourses)),
		"appearance:image":                    actionWithState(inv.imagePickAction("background")),
		"identity:avatar":                     actionWithState(inv.imagePickAction("avatar")),
		"zhiyun:export":                       actionWithState(inv.handleZhiyunExport),
		"zhiyun:login":                        inv.zhiyunControlAction,
		"zhiyun:refresh":                      inv.zhiyunControlAction,
		"zhiyun:back":                         inv.zhiyunControlAction,
		"zhiyun:bounds":                       inv.zhiyunControlAction,
		"library:open":                        actionWithState(actionStoreState(inv.handleLibraryOpen)),
		"library:open-file":                   actionWithState(inv.handleLibraryOpenFile),
		"library:reveal":                      actionWithState(inv.handleLibraryReveal),
		"media:open":                          actionWithState(inv.handleMediaOpen),
		"library:delete":                      actionWithState(inv.handleLibraryDelete),
		"zhiyun:search":                       actionWithState(inv.handleZhiyunSearch),
		"zhiyun:lessons":                      actionWithState(inv.handleZhiyunLessons),
		"textbook:pick":                       actionWithState(actionStoreState(inv.handleTextbookPick)),
		"textbook:bytes":                      actionWithState(actionParamsOnly(inv.handleTextbookBytes)),
		"textbook:open":                       actionWithState(actionStoreParams(inv.handleTextbookOpen)),
		"textbook:manual":                     actionWithState(inv.handleTextbookManual),
		"textbook:clear":                      actionWithState(actionStateParams(inv.handleTextbookClear)),
		"textbook:apply":                      actionWithState(actionStateParams(inv.handleTextbookApply)),
		"textbook:analyze":                    actionWithState(inv.handleTextbookAnalyze),
		"textbook:analyze-images":             actionWithState(inv.handleTextbookAnalyzeImages),
		"textbook:ppt-source":                 actionWithState(actionStateParams(inv.handleTextbookPptSource)),
		"textbook:ppt-ocr":                    actionWithState(inv.handleTextbookPptOcr),
		"settings:models":                     actionWithState(inv.handleSettingsModels),
		"settings:probe":                      actionWithState(inv.handleSettingsProbe),
		"settings:test":                       actionWithState(inv.handleSettingsTest),
		"settings:save":                       actionWithState(inv.handleSettingsSave),
		"chat":                                actionWithState(inv.handleChat),
		"cancel":                              actionWithState(inv.handleCancel),
		"job:current":                         actionWithState(inv.handleJobCurrent),
		"knowledge:ask":                       actionWithState(inv.handleKnowledgeAsk),
		"knowledge:classification-suggest":    actionWithState(inv.handleKnowledgeClassificationSuggest),
		"knowledge:vector-status":             actionWithState(inv.handleKnowledgeVectorStatus),
		"knowledge:vector-enable":             actionWithState(inv.handleKnowledgeVectorEnable),
		"knowledge:vector-rebuild":            actionWithState(inv.handleKnowledgeVectorRebuild),
		"knowledge:list":                      actionWithState(inv.handleKnowledgeList),
		"knowledge:generation":                actionWithState(inv.handleKnowledgeGeneration),
		"knowledge:read":                      actionWithState(inv.handleKnowledgeRead),
		"knowledge:import":                    actionWithState(inv.handleKnowledgeImport),
		"knowledge:citation":                  actionWithState(inv.handleKnowledgeCitation),
		"knowledge:delete":                    actionWithState(inv.handleKnowledgeDelete),
		"knowledge:organize":                  actionWithState(inv.handleKnowledgeOrganize),
		"knowledge:rebuild":                   actionWithState(inv.handleKnowledgeRebuild),
		"knowledge:search":                    actionWithState(inv.handleKnowledgeSearch),
		"knowledge:classification-options":    actionWithState(inv.handleKnowledgeClassificationOptions),
		"knowledge:classification-read":       actionWithState(inv.handleKnowledgeClassificationRead),
		"knowledge:classification-save":       actionWithState(inv.handleKnowledgeClassificationSave),
		"media:options":                       actionWithState(inv.handleMediaOptions),
		"media:list":                          actionWithState(inv.handleMediaList),
		"media:retry":                         actionWithState(inv.handleMediaRetry),
		"media:cancel":                        actionWithState(inv.handleMediaCancel),
		"media:file":                          actionWithState(inv.handleMediaFile),
		"media:auth-import":                   actionWithState(inv.handleMediaAuthImport),
		"media:auth-list":                     actionWithState(inv.handleMediaAuthList),
		"media:auth-remove":                   actionWithState(inv.handleMediaAuthRemove),
		"lecture:create":                      actionWithState(inv.handleLectureCreate),
		"lecture:transcript":                  actionWithState(inv.handleLectureTranscript),
		"lecture:courseware":                  actionWithState(inv.handleLectureCourseware),
		"questions:save":                      actionWithState(inv.handleQuestionsSave),
	}
}

func (inv *Invoker) invokeNative(store contract.StateStore, action string, params json.RawMessage) (any, error) {
	state := store.State()
	if state == nil {
		return nil, contract.Unavailable(msgHandshakePending)
	}
	return inv.callNative(state, action, params)
}

func (inv *Invoker) imagePickAction(kind string) func(contract.StateStore, *contract.State, json.RawMessage) (any, error) {
	return func(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
		return inv.handleImagePick(store, state, kind)
	}
}

func (inv *Invoker) zhiyunControlAction(store contract.StateStore, action string, params json.RawMessage) (any, error) {
	state := store.State()
	if state == nil {
		return nil, contract.Unavailable(msgHandshakePending)
	}
	return inv.handleZhiyunControl(store, action, params)
}

func (inv *Invoker) Invoke(store contract.StateStore, action string, params json.RawMessage) (any, error) {
	if handler, ok := inv.actions[action]; ok {
		return handler(store, action, params)
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
	if input.ConfigRoot == "" {
		input.ConfigRoot = input.DataRoot
	}
	if !isAbsPath(input.DataRoot) || !isAbsPath(input.ConfigRoot) || !isAbsPath(input.LibraryRoot) || !isAbsPath(input.ResourcesRoot) || !isAbsPath(input.PromptsRoot) {
		return nil, contract.InputInvalid("目录无效")
	}
	if input.PreviousLibraryRoot != "" && !isAbsPath(input.PreviousLibraryRoot) {
		return nil, contract.InputInvalid("旧资料目录无效")
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
		ConfigRoot:      input.ConfigRoot,
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
	if input.PreviousLibraryRoot != "" {
		if err := migrateLibraryPaths(state, input.PreviousLibraryRoot); err != nil {
			return nil, err
		}
	}
	store.SetState(state)
	slog.Info("hello accepted", "dataRoot", state.DataRoot)
	return state, nil
}

func migrateLibraryPaths(state *contract.State, previousRoot string) error {
	if err := prepareDatabase(state); err != nil {
		return err
	}
	database := filepath.Join(state.DataRoot, "assistant.db")
	write, err := storage.OpenWrite(database)
	if err != nil {
		return contract.Unavailable("数据库无法打开")
	}
	if err := storage.BootstrapInto(write); err != nil {
		write.Close()
		return contract.Unavailable("数据库初始化失败")
	}
	if err := write.Checkpoint(); err != nil {
		write.Close()
		return contract.Unavailable("资料路径迁移失败")
	}
	write.Close()
	if err := storage.RelocateLibraryPaths(database, previousRoot, state.LibraryRoot); err != nil {
		return contract.Unavailable("资料路径迁移失败")
	}
	return nil
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
