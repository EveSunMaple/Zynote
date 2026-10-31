package videoppt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

var operationIDPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type VideoOperation struct {
	Version    int    `json:"version"`
	Stage      string `json:"stage"`
	OutputName string `json:"outputName,omitempty"`
	Page       int    `json:"page,omitempty"`
	Path       string `json:"path,omitempty"`
	SourceHash string `json:"sourceHash,omitempty"`
	URL        string `json:"url,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	CourseName string `json:"courseName,omitempty"`
}

type CompletedOperationError struct {
	Result json.RawMessage
	Path   string
}

func (e *CompletedOperationError) Error() string {
	return "任务已经完成，成果已保存：" + e.Path
}

type operationCompletion struct {
	Operation VideoOperation  `json:"operation"`
	Result    json.RawMessage `json:"result"`
	Path      string          `json:"path"`
	SHA256    string          `json:"sha256"`
}

func (service *Service) CompleteOperation(task CreatedTask, result any, path string) error {
	operation, err := service.Operation(task.ID)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	hash, err := VideoDigest(path)
	if err != nil {
		return err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	directory := filepath.Join(service.Root, ".completed")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	return saveTaskJSON(filepath.Join(directory, task.ID+".json"), operationCompletion{operation, body, path, hash})
}

func VideoDigest(path string) (string, error) {
	input, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer input.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (service *Service) CreateOperationTask(id, name string, operation VideoOperation) (CreatedTask, error) {
	if id == "" {
		id = ids.New()
	}
	if !operationIDPattern.MatchString(id) {
		return CreatedTask{}, requestError("任务编号无效")
	}
	completedPath := filepath.Join(service.Root, ".completed", id+".json")
	if body, err := os.ReadFile(completedPath); err == nil {
		var completed operationCompletion
		if json.Unmarshal(body, &completed) != nil || completed.SHA256 == "" {
			return CreatedTask{}, requestError("已完成任务记录无效，请保留成果")
		}
		wanted, _ := json.Marshal(operation)
		bound, _ := json.Marshal(completed.Operation)
		if !bytes.Equal(wanted, bound) {
			return CreatedTask{}, requestError("任务编号已绑定其他来源或参数，请新建任务")
		}
		hash, err := VideoDigest(completed.Path)
		if err != nil || hash != completed.SHA256 {
			return CreatedTask{}, requestError("已完成任务成果已改变，不能重新生成，请核对原文件")
		}
		return CreatedTask{}, &CompletedOperationError{Result: completed.Result, Path: completed.Path}
	} else if !os.IsNotExist(err) {
		return CreatedTask{}, err
	}
	if operation.Version != 1 || (operation.Stage != "source" && operation.Stage != "auto" && operation.Stage != "subtitle") {
		return CreatedTask{}, requestError("任务来源记录无效")
	}
	if operation.Stage == "subtitle" && (operation.OutputName == "" || !subtitleFilePattern.MatchString(operation.OutputName) || operation.Page < 1) {
		return CreatedTask{}, requestError("字幕任务参数无效")
	}

	if err := os.MkdirAll(service.Root, 0755); err != nil {
		return CreatedTask{}, err
	}
	directory := filepath.Join(service.Root, id)
	task := CreatedTask{ID: id, Name: name, Dir: directory, Path: filepath.Join(directory, videoFileName)}
	body, _ := json.Marshal(operation)
	if err := os.Mkdir(directory, 0755); err != nil {
		if !os.IsExist(err) {
			return CreatedTask{}, err
		}
		if _, err := service.location(id); err != nil {
			return CreatedTask{}, err
		}
		existing, err := os.ReadFile(filepath.Join(directory, "operation.json"))
		if err != nil {
			return CreatedTask{}, requestError("任务来源正在准备或记录不完整，请保留后重试")
		}
		if !bytes.Equal(existing, body) {
			return CreatedTask{}, requestError("任务编号已绑定其他来源或参数，请新建任务")
		}
		raw, err := os.ReadFile(filepath.Join(directory, taskFileName))
		if err != nil {
			return CreatedTask{}, err
		}
		var meta struct{ ID, Name string }
		if json.Unmarshal(raw, &meta) != nil || meta.ID != id {
			return CreatedTask{}, requestError("任务记录不一致")
		}
		task.Name = meta.Name
		return task, nil
	}
	// Retain even an incomplete new directory on failure: another identical
	// caller may already be observing it. Never remove a shared operation here.
	if err := saveTaskJSON(filepath.Join(directory, "operation.json"), operation); err != nil {
		return CreatedTask{}, err
	}
	meta := map[string]any{"id": id, "name": name, "url": operation.URL}
	if operation.Stage == "subtitle" {
		meta["kind"] = "subtitle"
		meta["outputName"] = operation.OutputName
		meta["courseName"] = operation.CourseName
		meta["page"] = operation.Page
	}
	if err := saveTaskJSON(filepath.Join(directory, taskFileName), meta); err != nil {
		return CreatedTask{}, err
	}
	return task, nil
}

func (service *Service) Operation(id string) (VideoOperation, error) {
	if !operationIDPattern.MatchString(id) {
		return VideoOperation{}, requestError("任务编号无效")
	}
	directory, err := service.location(id)
	if err != nil {
		body, readErr := os.ReadFile(filepath.Join(service.Root, ".completed", id+".json"))
		var completed operationCompletion
		if readErr == nil && json.Unmarshal(body, &completed) == nil && completed.Operation.Version == 1 {
			return completed.Operation, nil
		}
		return VideoOperation{}, err
	}
	body, err := os.ReadFile(filepath.Join(directory, "operation.json"))
	if err != nil {
		return VideoOperation{}, err
	}
	var operation VideoOperation
	if json.Unmarshal(body, &operation) != nil || operation.Version != 1 || (operation.Stage != "source" && operation.Stage != "auto" && operation.Stage != "subtitle") {
		return VideoOperation{}, requestError("任务来源记录无效")
	}
	return operation, nil
}

func MarkVideoReady(task CreatedTask) error {
	hash, err := VideoDigest(task.Path)
	if err != nil {
		return err
	}
	return saveTaskJSON(filepath.Join(task.Dir, "video-ready.json"), map[string]any{"version": 1, "sha256": hash})
}

// CopyVideoReady persists the verified source digest before publication. A
// process loss after the link can therefore verify/adopt only those exact bytes.
func CopyVideoReady(ctx context.Context, source, destination string) error {
	if _, err := os.Lstat(destination); err == nil {
		return os.ErrExist
	} else if !os.IsNotExist(err) {
		return err
	}
	hash, err := VideoDigest(source)
	if err != nil {
		return err
	}
	task := CreatedTask{Dir: filepath.Dir(destination), Path: destination}
	if err := saveTaskJSON(filepath.Join(task.Dir, "video-ready.json"), map[string]any{"version": 1, "sha256": hash}); err != nil {
		return err
	}
	if err := CopyVideo(ctx, source, destination); err != nil {
		return err
	}
	return VerifyVideoReady(task)
}

func VerifyVideoReady(task CreatedTask) error {
	_, err := verifiedVideoDigest(task)
	return err
}

func verifiedVideoDigest(task CreatedTask) (string, error) {
	body, err := os.ReadFile(filepath.Join(task.Dir, "video-ready.json"))
	if err != nil {
		return "", requestError("视频副本缺少完成记录，请保留任务并核对来源")
	}
	var ready struct {
		Version int    `json:"version"`
		SHA256  string `json:"sha256"`
	}
	if json.Unmarshal(body, &ready) != nil || ready.Version != 1 || ready.SHA256 == "" {
		return "", requestError("视频副本完成记录无效")
	}
	hash, err := VideoDigest(task.Path)
	if err != nil || hash != ready.SHA256 {
		return "", requestError("视频副本已经改变，请保留任务并核对来源")
	}
	return hash, nil
}
