package videoppt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

const (
	videoFileName  = "video.mp4"
	taskFileName   = "task.json"
	errTaskCleanup = "任务清理失败"
)

var subtitleFilePattern = regexp.MustCompile(`(?i)\.srt$`)

type SubtitleTask struct {
	ASRLanguage string `json:"asrLanguage,omitempty"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	OutputName  string `json:"outputName"`
	CourseName  string `json:"courseName,omitempty"`
	URL         string `json:"url,omitempty"`
	Page        int    `json:"page"`
	Saved       *struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"saved,omitempty"`
}

type CreatedTask struct {
	ID   string
	Name string
	Dir  string
	Path string
}

func copyFile(source, destination string) error {
	return CopyVideoReady(context.Background(), source, destination)
}

func (service *Service) CreateSubtitleTask(source, outputName, courseName string, page int) (CreatedTask, error) {
	absolute, err := filepath.Abs(source)
	if err != nil {
		return CreatedTask{}, requestError("所选源文件不可用")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return CreatedTask{}, requestError("所选源文件不可用")
	}
	id := ids.New()
	directory := filepath.Join(service.Root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return CreatedTask{}, requestError("任务目录不可创建")
	}
	video := filepath.Join(directory, videoFileName)
	if err := copyFile(absolute, video); err != nil {
		_ = os.RemoveAll(directory)
		return CreatedTask{}, requestError("视频复制失败")
	}
	task := SubtitleTask{
		ID: id, Name: filepath.Base(absolute), Kind: "subtitle",
		OutputName: outputName, CourseName: courseName, Page: page,
	}
	if err := saveTaskJSON(filepath.Join(directory, taskFileName), task); err != nil {
		_ = os.RemoveAll(directory)
		return CreatedTask{}, requestError(errTaskRecordWrite)
	}
	return CreatedTask{ID: id, Name: task.Name, Dir: directory, Path: video}, nil
}

func (service *Service) SubtitleTask(id string) (SubtitleTask, string, error) {
	directory, err := service.location(id)
	if err != nil {
		return SubtitleTask{}, "", err
	}
	raw, err := os.ReadFile(filepath.Join(directory, taskFileName))
	if err != nil {
		return SubtitleTask{}, "", err
	}
	var meta struct {
		ASRLanguage string `json:"asrLanguage"`
		ID          string `json:"id"`
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		OutputName  string `json:"outputName"`
		CourseName  string `json:"courseName"`
		URL         string `json:"url"`
		Page        int    `json:"page"`
		Saved       *struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"saved"`
	}
	if json.Unmarshal(raw, &meta) != nil || meta.Kind != "subtitle" {
		return SubtitleTask{}, "", requestError("这不是字幕转写任务")
	}
	if meta.ID != id || meta.Name == "" || !subtitleFilePattern.MatchString(meta.OutputName) ||
		meta.Page < 1 {
		return SubtitleTask{}, "", requestError("字幕任务记录不一致")
	}
	task := SubtitleTask{
		ID: meta.ID, Name: meta.Name, Kind: meta.Kind, OutputName: meta.OutputName,
		CourseName: meta.CourseName, URL: meta.URL, Page: meta.Page, Saved: meta.Saved, ASRLanguage: meta.ASRLanguage,
	}
	language, err := contract.NormalizeASRLanguage(meta.ASRLanguage)
	if err != nil {
		return SubtitleTask{}, "", err
	}
	if operation, err := service.Operation(id); err == nil {
		boundLanguage, languageErr := contract.NormalizeASRLanguage(operation.ASRLanguage)
		if languageErr != nil || boundLanguage != language {
			return SubtitleTask{}, "", requestError("任务转写语言与来源绑定不一致")
		}
	} else if !os.IsNotExist(err) {
		return SubtitleTask{}, "", err
	}
	return task, directory, nil
}

func (service *Service) MarkSubtitleSaved(id, path string) error {
	task, directory, err := service.SubtitleTask(id)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return requestError("字幕文件不可读")
	}
	sum := sha256.Sum256(body)
	task.Saved = &struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}{Path: path, SHA256: hex.EncodeToString(sum[:])}
	if err := saveTaskJSON(filepath.Join(directory, taskFileName), task); err != nil {
		return requestError("字幕任务记录写入失败")
	}
	return nil
}

func (service *Service) DiscardCompleted(id string) error {
	directory, err := service.location(id)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return requestError(errTaskCleanup)
	}
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return requestError("任务目录含未知文件，暂不清理")
		}
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return requestError(errTaskCleanup)
		}
	}
	if err := os.Remove(directory); err != nil {
		return requestError(errTaskCleanup)
	}
	return nil
}

func SubtitleTitle(name string) string {
	extension := filepath.Ext(name)
	return strings.TrimSpace(strings.TrimSuffix(name, extension))
}

func (service *Service) VideoTask(id string) (CreatedTask, error) {
	directory, err := service.location(id)
	if err != nil {
		return CreatedTask{}, err
	}
	raw, err := os.ReadFile(filepath.Join(directory, taskFileName))
	if err != nil {
		return CreatedTask{}, err
	}
	var meta struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	if json.Unmarshal(raw, &meta) != nil || meta.ID != id || meta.Kind == "subtitle" || meta.Name == "" {
		return CreatedTask{}, requestError("这不是视频提取任务")
	}
	video := filepath.Join(directory, videoFileName)
	if info, err := os.Stat(video); err != nil || !info.Mode().IsRegular() {
		return CreatedTask{}, requestError("视频文件无效")
	}
	return CreatedTask{ID: id, Name: meta.Name, Dir: directory, Path: video}, nil
}

func (service *Service) CreateBlankTask(name string, meta map[string]any) (CreatedTask, error) {
	if strings.TrimSpace(name) == "" {
		return CreatedTask{}, requestError("任务名称无效")
	}
	id := ids.New()
	directory := filepath.Join(service.Root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return CreatedTask{}, requestError("任务目录不可创建")
	}
	record := map[string]any{"id": id, "name": name}
	for key, value := range meta {
		record[key] = value
	}
	if err := saveTaskJSON(filepath.Join(directory, taskFileName), record); err != nil {
		_ = os.RemoveAll(directory)
		return CreatedTask{}, requestError(errTaskRecordWrite)
	}
	return CreatedTask{ID: id, Name: name, Dir: directory, Path: filepath.Join(directory, videoFileName)}, nil
}
