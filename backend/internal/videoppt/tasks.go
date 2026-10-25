package videoppt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

var subtitleFilePattern = regexp.MustCompile(`(?i)\.srt$`)

type SubtitleTask struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	OutputName string `json:"outputName"`
	CourseName string `json:"courseName,omitempty"`
	URL        string `json:"url,omitempty"`
	Page       int    `json:"page"`
	Saved      *struct {
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
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		_ = os.Remove(destination)
		return err
	}
	return output.Close()
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
	video := filepath.Join(directory, "video.mp4")
	if err := copyFile(absolute, video); err != nil {
		_ = os.RemoveAll(directory)
		return CreatedTask{}, requestError("视频复制失败")
	}
	task := SubtitleTask{
		ID: id, Name: filepath.Base(absolute), Kind: "subtitle",
		OutputName: outputName, CourseName: courseName, Page: page,
	}
	encoded, _ := json.Marshal(task)
	temporary := filepath.Join(directory, "task.json.tmp")
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		_ = os.RemoveAll(directory)
		return CreatedTask{}, requestError("任务记录写入失败")
	}
	if err := os.Rename(temporary, filepath.Join(directory, "task.json")); err != nil {
		_ = os.RemoveAll(directory)
		return CreatedTask{}, requestError("任务记录写入失败")
	}
	return CreatedTask{ID: id, Name: task.Name, Dir: directory, Path: video}, nil
}

func (service *Service) SubtitleTask(id string) (SubtitleTask, string, error) {
	directory, err := service.location(id)
	if err != nil {
		return SubtitleTask{}, "", err
	}
	raw, err := os.ReadFile(filepath.Join(directory, "task.json"))
	if err != nil {
		return SubtitleTask{}, "", err
	}
	var meta struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		OutputName string `json:"outputName"`
		CourseName string `json:"courseName"`
		URL        string `json:"url"`
		Page       int    `json:"page"`
		Saved      *struct {
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
		CourseName: meta.CourseName, URL: meta.URL, Page: meta.Page, Saved: meta.Saved,
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
	encoded, _ := json.Marshal(task)
	temporary := filepath.Join(directory, "task-result.tmp")
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return requestError("字幕任务记录写入失败")
	}
	if err := os.Rename(temporary, filepath.Join(directory, "task.json")); err != nil {
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
		return requestError("任务清理失败")
	}
	for _, entry := range entries {
		info, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return requestError("任务目录含未知文件，暂不清理")
		}
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return requestError("任务清理失败")
		}
	}
	if err := os.Remove(directory); err != nil {
		return requestError("任务清理失败")
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
	raw, err := os.ReadFile(filepath.Join(directory, "task.json"))
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
	video := filepath.Join(directory, "video.mp4")
	if info, err := os.Stat(video); err != nil || !info.Mode().IsRegular() {
		return CreatedTask{}, requestError("视频文件无效")
	}
	return CreatedTask{ID: id, Name: meta.Name, Dir: directory, Path: video}, nil
}
