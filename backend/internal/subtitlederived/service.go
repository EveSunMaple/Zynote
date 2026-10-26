package subtitlederived

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlesync"
)

type Source struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	Path       string `json:"path"`
	Hash       string `json:"hash"`
	Generation string `json:"generation"`
}

type Output struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type Point struct {
	Cue       float64 `json:"cue"`
	Quote     string  `json:"quote"`
	Point     string  `json:"point"`
	Topic     string  `json:"topic"`
	Timestamp string  `json:"timestamp"`
}

type Prepared struct {
	Markdown string  `json:"markdown"`
	Points   []Point `json:"points"`
}

type Item struct {
	Source   Source    `json:"source"`
	Mode     string    `json:"mode"`
	Status   string    `json:"status"`
	Error    string    `json:"error,omitempty"`
	Outputs  []Output  `json:"outputs,omitempty"`
	Prepared *Prepared `json:"prepared,omitempty"`
}

type Task struct {
	ID          string `json:"id"`
	CreatedAt   string `json:"createdAt"`
	CourseName  string `json:"courseName,omitempty"`
	Interrupted bool   `json:"interrupted"`
	Items       []Item `json:"items"`
}

type Dependencies struct {
	Generation func(ctx context.Context, url string) (string, error)
	Summarize  func(ctx context.Context, srt, mode string) (string, []Point, error)
	RenderSVG  func(title string, points []Point) string
	Progress   func(message string)
}

type Service struct {
	Root    string
	Library string
}

func New(root, libraryRoot string) *Service {
	return &Service{Root: root, Library: libraryRoot}
}

var modeTitles = map[string]string{
	"brief": "简要摘要", "detailed": "详细摘要", "keypoints": "课程要点", "mindmap": "思维导图",
}

func validMode(value string) bool {
	_, ok := modeTitles[value]
	return ok
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func inside(root, target string) bool {
	base, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(base, absolute)
	return err == nil && relative != "" && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (service *Service) save(task Task) error {
	if err := os.MkdirAll(service.Root, 0o755); err != nil {
		return fmt.Errorf("笔记任务目录不可用")
	}
	encoded, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(service.Root, task.ID+".json")
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return fmt.Errorf("笔记任务保存失败")
	}
	return os.Rename(temporary, path)
}

func (service *Service) Read(id string) (Task, error) {
	if !validUUID(id) {
		return Task{}, fmt.Errorf("笔记任务标识无效")
	}
	raw, err := os.ReadFile(filepath.Join(service.Root, id+".json"))
	if err != nil {
		return Task{}, fmt.Errorf("笔记任务不存在")
	}
	var task Task
	if json.Unmarshal(raw, &task) != nil || task.ID != id || len(task.Items) < 1 || len(task.Items) > 4000 {
		return Task{}, fmt.Errorf("笔记任务身份不一致")
	}
	return task, nil
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
				return false
			}
		}
	}
	return true
}

func (service *Service) List() ([]Task, error) {
	if err := os.MkdirAll(service.Root, 0o755); err != nil {
		return nil, fmt.Errorf("笔记任务目录不可用")
	}
	entries, err := os.ReadDir(service.Root)
	if err != nil {
		return nil, fmt.Errorf("笔记任务目录不可用")
	}
	result := []Task{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || !validUUID(strings.TrimSuffix(name, ".json")) {
			continue
		}
		task, err := service.Read(strings.TrimSuffix(name, ".json"))
		if err == nil {
			result = append(result, task)
		}
	}
	sort.SliceStable(result, func(left, right int) bool { return result[left].CreatedAt > result[right].CreatedAt })
	return result, nil
}

func (service *Service) Create(sources []Source, modes []string, courseName string) (Task, error) {
	if len(sources) < 1 || len(sources) > 1000 || len(modes) < 1 || len(modes) > 4 {
		return Task{}, fmt.Errorf("不要重复选择分集或笔记形式")
	}
	seen := map[string]bool{}
	checked := []Source{}
	for _, source := range sources {
		locator, err := subtitlesync.Locator(source.URL)
		if err != nil || source.Title == "" || len([]rune(source.Title)) > 200 ||
			!inside(service.Library, source.Path) || len(source.Hash) != 64 || source.Generation == "" {
			return Task{}, fmt.Errorf("不要重复选择分集或笔记形式")
		}
		if seen[locator] {
			return Task{}, fmt.Errorf("不要重复选择分集或笔记形式")
		}
		seen[locator] = true
		source.URL = locator
		checked = append(checked, source)
	}
	modeSeen := map[string]bool{}
	for _, mode := range modes {
		if !validMode(mode) || modeSeen[mode] {
			return Task{}, fmt.Errorf("不要重复选择分集或笔记形式")
		}
		modeSeen[mode] = true
	}
	task := Task{
		ID: ids.New(), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		CourseName: courseName, Interrupted: true,
	}
	for _, source := range checked {
		for _, mode := range modes {
			task.Items = append(task.Items, Item{Source: source, Mode: mode, Status: "等待处理"})
		}
	}
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (service *Service) current(source Source, dependencies Dependencies, ctx context.Context) (string, error) {
	if !inside(service.Library, source.Path) {
		return "", fmt.Errorf("笔记来源或成果不在课程资料目录")
	}
	body, err := os.ReadFile(source.Path)
	if err != nil {
		return "", err
	}
	text := string(body)
	if digest(text) != source.Hash {
		return "", fmt.Errorf("字幕来源已有改变，请重新同步并建立笔记任务；旧成果保留")
	}
	if dependencies.Generation != nil {
		generation, err := dependencies.Generation(ctx, source.URL)
		if err != nil || generation != source.Generation {
			return "", fmt.Errorf("字幕来源已有改变，请重新同步并建立笔记任务；旧成果保留")
		}
	}
	if _, err := documents.ParseSrt(text); err != nil {
		return "", err
	}
	return text, nil
}

func (service *Service) Run(ctx context.Context, id string, dependencies Dependencies) (Task, error) {
	task, err := service.Read(id)
	if err != nil {
		return Task{}, err
	}
	task.Interrupted = true
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	defer func() { _ = service.save(task) }()
	for index := range task.Items {
		if err := ctx.Err(); err != nil {
			return Task{}, err
		}
		item := &task.Items[index]
		if dependencies.Progress != nil {
			dependencies.Progress(fmt.Sprintf(
				"整理所选笔记 %d/%d · %s · %s", index+1, len(task.Items), item.Source.Title, modeTitles[item.Mode],
			))
		}
		created := []string{}
		if err := func() error {
			srt, err := service.current(item.Source, dependencies, ctx)
			if err != nil {
				return err
			}
			if len(item.Outputs) > 0 {
				for _, output := range item.Outputs {
					if !inside(service.Library, output.Path) {
						return fmt.Errorf("笔记来源或成果不在课程资料目录")
					}
					body, err := os.ReadFile(output.Path)
					if err != nil || digest(string(body)) != output.Hash {
						return fmt.Errorf("已保存笔记被修改或缺失，请核对原文件；未自动覆盖")
					}
				}
				item.Status = "已完成"
				item.Error = ""
				return nil
			}
			if item.Prepared == nil {
				if dependencies.Summarize == nil {
					return fmt.Errorf("摘要不可用")
				}
				markdown, points, err := dependencies.Summarize(ctx, srt, item.Mode)
				if err != nil {
					return err
				}
				item.Prepared = &Prepared{Markdown: markdown, Points: points}
				if _, err := service.current(item.Source, dependencies, ctx); err != nil {
					return err
				}
				if err := service.save(task); err != nil {
					return err
				}
			}
			if _, err := service.current(item.Source, dependencies, ctx); err != nil {
				return err
			}
			directory, err := library.CourseDirectory(service.Library, "Notes", task.CourseName)
			if err != nil {
				return err
			}
			title := []rune(library.FolderName(item.Source.Title))
			if len(title) > 40 {
				title = title[:40]
			}
			stem := string(title) + "-" + modeTitles[item.Mode] + "-" + task.ID[:8] + "-" + itoa(index+1)
			provenance := "\n\n---\n字幕来源：" + item.Source.URL + "\n来源版本：" + item.Source.Generation +
				"\n字幕 SHA256：" + item.Source.Hash + "\n"
			files := []struct {
				Path string
				Text string
			}{
				{filepath.Join(directory, stem+".md"), item.Prepared.Markdown + provenance},
				{filepath.Join(directory, stem+".json"), prettyJSON(item.Prepared.Points)},
			}
			if item.Mode == "mindmap" && dependencies.RenderSVG != nil {
				files = append(files, struct {
					Path string
					Text string
				}{filepath.Join(directory, stem+".svg"), dependencies.RenderSVG(item.Source.Title, item.Prepared.Points)})
			}
			for _, file := range files {
				existing, err := os.ReadFile(file.Path)
				if err == nil {
					if string(existing) != file.Text {
						return fmt.Errorf("同名成果已有不同内容，未覆盖，请检查任务记录")
					}
				} else if os.IsNotExist(err) {
					if err := os.WriteFile(file.Path, []byte(file.Text), 0o644); err != nil {
						return err
					}
					created = append(created, file.Path)
				} else {
					return err
				}
				body, err := os.ReadFile(file.Path)
				if err != nil || string(body) != file.Text {
					return fmt.Errorf("笔记保存校验失败")
				}
			}
			if _, err := service.current(item.Source, dependencies, ctx); err != nil {
				return err
			}
			item.Outputs = make([]Output, 0, len(files))
			for _, file := range files {
				item.Outputs = append(item.Outputs, Output{Path: file.Path, Hash: digest(file.Text)})
			}
			item.Status = "已完成"
			item.Error = ""
			item.Prepared = nil
			return nil
		}(); err != nil {
			for _, path := range created {
				_ = os.Remove(path)
			}
			if ctx.Err() != nil {
				return Task{}, ctx.Err()
			}
			item.Status = "失败"
			item.Error = err.Error()
		}
		if err := service.save(task); err != nil {
			return Task{}, err
		}
	}
	task.Interrupted = false
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func prettyJSON(value any) string {
	encoded, _ := json.MarshalIndent(value, "", "  ")
	return string(encoded)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
