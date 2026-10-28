package videoppt

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	errTaskPreviewMismatch = "任务预览不一致"
	errTaskRecordCorrupt   = "任务记录损坏或不一致，已保留原文件，请查看任务文件。"
)

var taskIDPattern = regexp.MustCompile(`^[\da-f-]{36}$`)
var frameFilePattern = regexp.MustCompile(`^frame-\d+\.png$`)

type Region struct {
	Left   float64 `json:"left"`
	Right  float64 `json:"right"`
	Top    float64 `json:"top"`
	Bottom float64 `json:"bottom"`
}

type Source struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Duration float64 `json:"duration"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	Preview  string  `json:"preview"`
	Crop     Region  `json:"crop"`
	Warning  string  `json:"warning"`
}

type Page struct {
	Page        int     `json:"page"`
	Time        float64 `json:"time"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
	Preview     string  `json:"preview"`
	Crop        Region  `json:"crop"`
	Keep        bool    `json:"keep"`
	Reason      string  `json:"reason"`
	DuplicateOf *int    `json:"duplicateOf,omitempty"`
}

type Review struct {
	ID         string `json:"id"`
	SourceName string `json:"sourceName"`
	Warning    string `json:"warning,omitempty"`
	Pages      []Page `json:"pages"`
}

type Pending struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	SavedPath string `json:"savedPath,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Service struct{ Root string }

func New(root string) *Service { return &Service{Root: root} }

func requestError(message string) error { return &RequestError{Message: message} }

type RequestError struct{ Message string }

func (value *RequestError) Error() string { return value.Message }

func validCrop(region Region) bool {
	values := []float64{region.Left, region.Right, region.Top, region.Bottom}
	for _, value := range values {
		if value < 0 || value > 1 || math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return region.Right-region.Left >= 0.1 && region.Bottom-region.Top >= 0.1
}

func (service *Service) location(id string) (string, error) {
	if !taskIDPattern.MatchString(id) {
		return "", requestError("任务编号无效")
	}
	directory := filepath.Join(service.Root, id)
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", requestError("任务目录无效")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return "", requestError("任务目录无效")
	}
	return directory, nil
}

func (service *Service) Directory(id string) (string, error) { return service.location(id) }

func (service *Service) Restore(id string) (map[string]any, error) {
	directory, err := service.location(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(directory, "source.json"))
	if err != nil {
		return nil, err
	}
	var source Source
	if json.Unmarshal(raw, &source) != nil || source.ID != id || source.Name == "" ||
		source.Duration <= 0 || source.Width < 1 || source.Height < 1 || source.Preview == "" ||
		!validCrop(source.Crop) {
		return nil, requestError("任务资料不一致")
	}
	frames, loaded, err := readFrames(directory)
	if err != nil {
		return nil, err
	}
	var review *Review
	if loaded {
		review, err = readReview(directory, id, frames)
		if err != nil {
			return nil, err
		}
	}
	result := map[string]any{"source": source, "review": nil}
	if review != nil {
		result["review"] = review
	}
	return result, nil
}

type storedFrame struct {
	Page int     `json:"page"`
	Time float64 `json:"time"`
	File string  `json:"file"`
}

func readFrames(directory string) ([]storedFrame, bool, error) {
	raw, err := os.ReadFile(filepath.Join(directory, "frames.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var frames []storedFrame
	if json.Unmarshal(raw, &frames) != nil {
		return nil, true, requestError(errTaskPreviewMismatch)
	}
	for _, frame := range frames {
		if frame.Page < 1 || frame.Time < 0 || !filePatternFrame(frame.File) {
			return nil, true, requestError(errTaskPreviewMismatch)
		}
	}
	return frames, true, nil
}

func filePatternFrame(value string) bool {
	return frameFilePattern.MatchString(value)
}

func readReview(directory, id string, frames []storedFrame) (*Review, error) {
	raw, err := os.ReadFile(filepath.Join(directory, "review.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var review Review
	if json.Unmarshal(raw, &review) != nil {
		return nil, requestError(errTaskPreviewMismatch)
	}
	if review.ID != id || len(review.Pages) != len(frames) {
		return nil, requestError(errTaskPreviewMismatch)
	}
	for index, page := range review.Pages {
		if page.Page != frames[index].Page || page.Width <= 0 || page.Height <= 0 || !validCrop(page.Crop) {
			return nil, requestError(errTaskPreviewMismatch)
		}
	}
	return &review, nil
}

func (service *Service) Pending() ([]Pending, error) {
	if err := os.MkdirAll(service.Root, 0o755); err != nil {
		return nil, requestError("视频任务目录不可用")
	}
	entries, err := os.ReadDir(service.Root)
	if err != nil {
		return nil, requestError("视频任务目录不可用")
	}
	result := []Pending{}
	for _, entry := range entries {
		id := entry.Name()
		if !taskIDPattern.MatchString(id) {
			continue
		}
		pending, err := service.pendingOne(id)
		if err != nil {
			result = append(result, service.brokenTask(id, err))
			continue
		}
		result = append(result, pending)
	}
	return result, nil
}

func (service *Service) pendingOne(id string) (Pending, error) {
	directory, err := service.location(id)
	if err != nil {
		return Pending{}, err
	}
	raw, err := os.ReadFile(filepath.Join(directory, "task.json"))
	if err != nil {
		return Pending{}, err
	}
	var meta struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		OutputName string `json:"outputName"`
		Page       int    `json:"page"`
		Saved      *struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"saved"`
	}
	if json.Unmarshal(raw, &meta) != nil {
		return Pending{}, requestError(errTaskRecordCorrupt)
	}
	if meta.Kind == "subtitle" {
		if meta.ID != id || meta.Name == "" || meta.OutputName == "" || meta.Page < 1 {
			return Pending{}, requestError(errTaskRecordCorrupt)
		}
		pending := Pending{ID: id, Name: meta.Name, Kind: "subtitle"}
		if meta.Saved != nil && meta.Saved.Path != "" {
			pending.SavedPath = meta.Saved.Path
		}
		return pending, nil
	}
	restored, err := service.Restore(id)
	if err != nil {
		return Pending{}, err
	}
	source := restored["source"].(Source)
	return Pending{ID: id, Name: source.Name}, nil
}

func (service *Service) brokenTask(id string, err error) Pending {
	name := "未完成任务 " + id[:8]
	if directory, locationErr := service.location(id); locationErr == nil {
		if raw, readErr := os.ReadFile(filepath.Join(directory, "task.json")); readErr == nil {
			var meta struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &meta) == nil && meta.ID == id && strings.TrimSpace(meta.Name) != "" {
				name = meta.Name
			}
		}
	}
	message := errTaskRecordCorrupt
	if os.IsNotExist(err) {
		message = "任务准备未完成，已保留下载文件，请查看任务文件。"
	} else if os.IsPermission(err) {
		message = "任务文件无法读取，请检查文件访问权限。"
	}
	return Pending{ID: id, Name: name, Error: message}
}
