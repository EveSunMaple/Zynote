package visual

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	errTaskSaveFailed         = "画面任务保存失败"
	errRecognitionPathInvalid = "识别成果路径无效，保留当前文件"
)

const (
	StatusWaiting = "等待识别"
	StatusDone    = "已完成"
	StatusFailed  = "失败"
)

var idPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Frame struct {
	Path   string  `json:"path"`
	Time   float64 `json:"time"`
	Hash   string  `json:"hash"`
	Status string  `json:"status"`
	Text   string  `json:"text,omitempty"`
	Error  string  `json:"error,omitempty"`
}

type Journal struct {
	Output       string `json:"output"`
	Temp         string `json:"temp"`
	Hash         string `json:"hash"`
	PreviousHash string `json:"previousHash,omitempty"`
	Backup       string `json:"backup,omitempty"`
}

type Preparation struct {
	Cap    int    `json:"cap"`
	Policy string `json:"policy"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type Task struct {
	ID             string       `json:"id"`
	Source         string       `json:"source"`
	SourceHash     string       `json:"sourceHash"`
	Duration       float64      `json:"duration"`
	Frames         []Frame      `json:"frames"`
	Gaps           []any        `json:"gaps"`
	Connection     string       `json:"connection"`
	Output         string       `json:"output,omitempty"`
	OutputHash     string       `json:"outputHash,omitempty"`
	OutputJournal  *Journal     `json:"outputJournal,omitempty"`
	OutputPending  bool         `json:"outputPending,omitempty"`
	OutputRecovery string       `json:"outputRecovery,omitempty"`
	Cleaned        bool         `json:"cleaned,omitempty"`
	Preparation    *Preparation `json:"preparation,omitempty"`
}

type Capture struct {
	ID         string  `json:"id"`
	Source     string  `json:"source"`
	SourceHash string  `json:"sourceHash"`
	Duration   float64 `json:"duration"`
	Frames     []Frame `json:"frames"`
	Gaps       []any   `json:"gaps"`
}

func OutputRecoveryReason(task Task, currentHash string, hasOutput, hasLegacyOutput bool) string {
	if task.OutputJournal != nil {
		return "journal"
	}
	if task.OutputPending {
		return "pending"
	}
	if task.Output != "" && (task.OutputHash == "" || !hasOutput || currentHash != task.OutputHash) {
		return "changed"
	}
	if (hasLegacyOutput || task.Output == "" || !task.Cleaned) && len(task.Frames) > 0 {
		allDone := true
		for _, frame := range task.Frames {
			if frame.Status != StatusDone {
				allDone = false
				break
			}
		}
		if allDone {
			return "legacy"
		}
	}
	return ""
}

func (service *Service) load(id string) (Task, error) {
	if !idPattern.MatchString(id) {
		return Task{}, badRequest("画面任务标识无效")
	}
	raw, err := os.ReadFile(filepath.Join(service.Root, id+".json"))
	if err != nil {
		return Task{}, badRequest("画面任务不存在")
	}
	var task Task
	if json.Unmarshal(raw, &task) != nil {
		return Task{}, badRequest("画面任务记录无效")
	}
	if task.ID != id {
		return Task{}, badRequest("画面任务记录不一致")
	}
	return task, nil
}

func (service *Service) save(task Task) error {
	if err := os.MkdirAll(service.Root, 0o755); err != nil {
		return badRequest("画面任务目录不可用")
	}
	task.OutputRecovery = ""
	encoded, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return badRequest(errTaskSaveFailed)
	}
	path := filepath.Join(service.Root, task.ID+".json")
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return badRequest(errTaskSaveFailed)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return badRequest(errTaskSaveFailed)
	}
	return nil
}

func (service *Service) outputPath(task Task) (string, error) {
	output := task.Output
	if output == "" {
		output = filepath.Join(service.Root, task.ID+"-画面识别.md")
	}
	output, err := filepath.Abs(output)
	if err != nil {
		return "", badRequest(errRecognitionPathInvalid)
	}
	root, err := filepath.Abs(service.Root)
	if err != nil {
		return "", badRequest(errRecognitionPathInvalid)
	}
	relative, err := filepath.Rel(root, output)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", badRequest(errRecognitionPathInvalid)
	}
	return output, nil
}
