package contract

import (
	"encoding/json"
	"runtime"
)

const ProtocolVersion = 1

const (
	CodeInputInvalid = "INPUT_INVALID"
	CodeNotFound     = "NOT_FOUND"
	CodeConflict     = "CONFLICT"
	CodeBusy         = "BUSY"
	CodeCancelled    = "CANCELLED"
	CodeUnavailable  = "UNAVAILABLE"
	CodeInternal     = "INTERNAL"
)

type AppError struct {
	Code    string
	Message string
	Data    any
}

func (e *AppError) Error() string { return e.Code + ": " + e.Message }

func NewError(code, message string) *AppError { return &AppError{Code: code, Message: message} }

func InputInvalid(message string) *AppError { return NewError(CodeInputInvalid, message) }

func Unavailable(message string) *AppError { return NewError(CodeUnavailable, message) }

func NotFound(message string) *AppError { return NewError(CodeNotFound, message) }

func CurrentPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	case "windows":
		return "win32"
	}
	return runtime.GOOS
}

type State struct {
	Protocol        int      `json:"protocol"`
	Platform        string   `json:"platform"`
	PID             int      `json:"pid"`
	GoVersion       string   `json:"goVersion"`
	AppVersion      string   `json:"appVersion"`
	ElectronVersion string   `json:"electronVersion"`
	DataRoot        string   `json:"dataRoot"`
	ConfigRoot      string   `json:"configRoot"`
	AppRoot         string   `json:"appRoot"`
	LibraryRoot     string   `json:"libraryRoot"`
	ResourcesRoot   string   `json:"resourcesRoot"`
	PromptsRoot     string   `json:"promptsRoot"`
	Portable        bool     `json:"portable"`
	Packaged        bool     `json:"packaged"`
	Capabilities    []string `json:"capabilities"`
}

var DefaultCapabilities = []string{
	"app:info", "settings:get", "appearance:get", "appearance:save", "appearance:clear", "identity:get", "identity:save",
	"db:health", "db:backup", "settings:save", "chat", "cancel", "job:current", "settings:models", "settings:probe", "settings:test", "zhiyun:search", "zhiyun:lessons",
	"library:delete", "library:open", "library:open-file", "library:reveal", "media:open", "study:get", "study:save", "study:parse", "textbook:pick", "textbook:bytes", "textbook:open", "textbook:manual", "textbook:clear", "textbook:apply",
	"textbook:analyze", "textbook:analyze-images", "textbook:ppt-source", "textbook:ppt-ocr", "subtitles:generate", "subtitles:save", "subtitles:import", "questions:split", "practice:submit", "practice:reset", "practice:answer", "practice:prepare", "practice:generate", "notes:save", "notes:generate", "knowledge:list", "knowledge:generation", "knowledge:read", "knowledge:import",
	"knowledge:citation", "knowledge:delete", "knowledge:organize", "knowledge:rebuild",
	"knowledge:search", "knowledge:vector-status", "knowledge:vector-enable", "knowledge:vector-rebuild", "knowledge:ask", "knowledge:classification-suggest", "knowledge:classification-options", "knowledge:classification-read",
	"knowledge:classification-save",
	"course:detail", "library:list", "library:dir",
	"course:create", "sessions", "session:create",
	"db:bootstrap", "course:delete", "session:delete", "lecture:delete", "lecture:detail",
	"pdf.text", "pdf.plainText", "pdf.inspect", "pdf.write",
	"document:parse", "transcript:read", "files:add", "files:remove", "lecture:create", "lecture:transcript", "lecture:courseware", "questions:save",
	"visual:list", "visual:prepare", "visual:prepare-resume", "visual:recognize", "visual:recover-output", "export", "tools:request", "tools:skill-open", "im:draft", "runtime:install", "runtime:status", "courses", "appearance:image", "identity:avatar", "zhiyun:export", "zhiyun:tasks", "zhiyun:task-cancel", "zhiyun:task-retry", "zhiyun:login", "zhiyun:refresh", "zhiyun:back", "zhiyun:bounds", "subtitle-derived:list", "subtitle-derived:resume", "subtitle-derived:start", "subtitle-sync:list", "subtitle-sync:start", "subtitle-sync:mixed", "subtitle-sync:resume", "subtitle-sync:attach", "video-ppt:pending", "video-ppt:restore", "video-ppt:reveal", "video-ppt:subtitle-import", "video-ppt:subtitle-save", "video-ppt:subtitle-export", "video-ppt:series", "video-ppt:subtitle", "video-ppt:subtitle-transcribe-local", "video-ppt:resume-subtitle", "video-ppt:source", "video-ppt:extract", "video-ppt:export", "video-ppt:auto", "video-ppt:summary", "video-ppt:notes-export", "video-ppt:batch", "video-ppt:batch-subtitles", "video-ppt:subtitle-transcribe", "ppt-extract:analyze", "ppt-extract:export", "media:options", "media:list", "media:retry", "media:cancel", "media:file",
	"media:auth-import", "media:auth-list", "media:auth-remove",
	"bili-resources:open-page", "bili-resources:status", "bili-resources:browse", "bili-resources:qr", "bili-resources:poll",
	"bili-resources:logout", "bili-resources:folders", "bili-resources:videos", "media:catalog", "media:create", "media:discussion", "media:discussion-import",
}

type HelloParams struct {
	Protocol            int    `json:"protocol"`
	AppVersion          string `json:"appVersion"`
	ElectronVersion     string `json:"electronVersion"`
	Platform            string `json:"platform"`
	DataRoot            string `json:"dataRoot"`
	ConfigRoot          string `json:"configRoot"`
	AppRoot             string `json:"appRoot"`
	LibraryRoot         string `json:"libraryRoot"`
	PreviousLibraryRoot string `json:"previousLibraryRoot"`
	ResourcesRoot       string `json:"resourcesRoot"`
	PromptsRoot         string `json:"promptsRoot"`
	NativePath          string `json:"nativePath"`
	Portable            bool   `json:"portable"`
	Packaged            bool   `json:"packaged"`
}

type StateStore interface {
	State() *State
	SetState(*State)
	Reverse() ReverseCaller
}

type Handler interface {
	Invoke(action string, params json.RawMessage) (any, error)
}

type ReverseCaller interface {
	Call(method string, params any, timeoutMS int) (json.RawMessage, error)
}

type Notifier interface {
	Notify(method string, params any) error
}
