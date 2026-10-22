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
	LibraryRoot     string   `json:"libraryRoot"`
	ResourcesRoot   string   `json:"resourcesRoot"`
	PromptsRoot     string   `json:"promptsRoot"`
	Portable        bool     `json:"portable"`
	Capabilities    []string `json:"capabilities"`
}

var DefaultCapabilities = []string{
	"app:info", "settings:get", "appearance:get", "identity:get",
	"db:health", "db:backup", "course:detail", "library:list",
	"course:create", "sessions", "session:create",
}

type HelloParams struct {
	Protocol        int    `json:"protocol"`
	AppVersion      string `json:"appVersion"`
	ElectronVersion string `json:"electronVersion"`
	Platform        string `json:"platform"`
	DataRoot        string `json:"dataRoot"`
	LibraryRoot     string `json:"libraryRoot"`
	ResourcesRoot   string `json:"resourcesRoot"`
	PromptsRoot     string `json:"promptsRoot"`
	Portable        bool   `json:"portable"`
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
