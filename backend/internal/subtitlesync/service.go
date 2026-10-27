package subtitlesync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	"github.com/EveSunMaple/Zynote/backend/internal/media/bilibili"
)

type Item struct {
	Page         int     `json:"page"`
	Title        string  `json:"title"`
	URL          string  `json:"url"`
	Status       string  `json:"status"`
	Path         string  `json:"path,omitempty"`
	Hash         string  `json:"hash,omitempty"`
	Error        string  `json:"error,omitempty"`
	Unchanged    bool    `json:"unchanged,omitempty"`
	SourceMethod string  `json:"sourceMethod,omitempty"`
	Generation   *string `json:"generation,omitempty"`
}

type Task struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	CourseName  string `json:"courseName,omitempty"`
	CreatedAt   string `json:"createdAt"`
	Items       []Item `json:"items"`
	Interrupted bool   `json:"interrupted"`
}

type Dependencies struct {
	Acquire    func(ctx context.Context, item Item) (srt, path string, err error)
	Publish    func(ctx context.Context, item Item, srt, category string) (unchanged bool, generation string, err error)
	Generation func(ctx context.Context, url string) (string, error)
	Progress   func(message string)
}

type Service struct {
	Root    string
	Library string
	mu      sync.Mutex
}

func New(root, library string) *Service {
	return &Service{Root: root, Library: library}
}

func Locator(value string) (string, error) {
	normalized, err := bilibili.VideoURL(value)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", err
	}
	page := parsed.Query().Get("p")
	if page == "" {
		page = "1"
	}
	number, err := strconv.Atoi(page)
	if err != nil || number < 1 {
		return "", fmt.Errorf("视频分 P 编号无效")
	}
	return "https://www.bilibili.com" + strings.TrimSuffix(parsed.Path, "/") + "/?p=" + strconv.Itoa(number), nil
}

func (service *Service) save(task Task) error {
	if err := os.MkdirAll(service.Root, 0o755); err != nil {
		return fmt.Errorf("同步记录目录不可用")
	}
	encoded, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(service.Root, task.ID+".json")
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return fmt.Errorf("同步记录保存失败")
	}
	return os.Rename(temporary, path)
}

func (service *Service) read(id string) (Task, error) {
	if !validID(id) {
		return Task{}, fmt.Errorf("同步记录无效")
	}
	raw, err := os.ReadFile(filepath.Join(service.Root, id+".json"))
	if err != nil {
		return Task{}, fmt.Errorf("同步记录不存在")
	}
	var task Task
	if json.Unmarshal(raw, &task) != nil || task.ID != id || len(task.Items) < 1 || len(task.Items) > 1000 {
		return Task{}, fmt.Errorf("同步记录身份不一致")
	}
	return task, nil
}

func validID(value string) bool {
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
		return nil, fmt.Errorf("同步记录目录不可用")
	}
	entries, err := os.ReadDir(service.Root)
	if err != nil {
		return nil, fmt.Errorf("同步记录目录不可用")
	}
	result := []Task{}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || !validID(strings.TrimSuffix(name, ".json")) {
			continue
		}
		task, err := service.read(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		result = append(result, task)
	}
	sort.SliceStable(result, func(left, right int) bool { return result[left].CreatedAt > result[right].CreatedAt })
	return result, nil
}

type CreateItem struct {
	Page  int    `json:"page"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func (service *Service) Create(items []CreateItem, category, courseName string) (Task, error) {
	if len(items) < 1 || len(items) > 1000 {
		return Task{}, fmt.Errorf("不能重复选择同一分集")
	}
	task := Task{
		ID: ids.New(), Category: category, CourseName: courseName,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Interrupted: true,
	}
	seen := map[string]bool{}
	for _, item := range items {
		if item.Page < 1 || strings.TrimSpace(item.Title) == "" || len([]rune(item.Title)) > 200 {
			return Task{}, fmt.Errorf("所选分集无效")
		}
		locator, err := Locator(item.URL)
		if err != nil {
			return Task{}, err
		}
		if seen[locator] {
			return Task{}, fmt.Errorf("不能重复选择同一分集")
		}
		seen[locator] = true
		task.Items = append(task.Items, Item{
			Page: item.Page, Title: item.Title, URL: locator, Status: "等待处理",
		})
	}
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (service *Service) Attach(id string, selector any, path string) (Task, error) {
	task, err := service.read(id)
	if err != nil {
		return Task{}, err
	}
	matches := []int{}
	for index, item := range task.Items {
		matched := false
		switch value := selector.(type) {
		case string:
			locator, err := Locator(value)
			if err == nil && item.URL == locator {
				matched = true
			}
		case int:
			if item.Page == value {
				matched = true
			}
		}
		if matched {
			matches = append(matches, index)
		}
	}
	if len(matches) != 1 {
		return Task{}, fmt.Errorf("请按视频网址明确选择本次同步的分集")
	}
	absolute, err := filepath.Abs(path)
	if err != nil || !inside(service.Library, absolute) {
		return Task{}, fmt.Errorf("字幕不在课程资料目录")
	}
	body, err := os.ReadFile(absolute)
	if err != nil {
		return Task{}, fmt.Errorf("字幕文件不可读")
	}
	text := string(body)
	if _, err := documents.ParseSrt(text); err != nil {
		return Task{}, err
	}
	item := &task.Items[matches[0]]
	item.Path = absolute
	item.Hash = digest(text)
	item.SourceMethod = "用户指定 SRT"
	item.Status = "等待处理"
	item.Error = ""
	task.Interrupted = true
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	return task, nil
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

func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func (service *Service) Read(id string) (Task, error) {
	return service.read(id)
}

func (service *Service) prepareSyncTask(id string) (Task, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	task, err := service.read(id)
	if err != nil {
		return Task{}, err
	}
	task.Interrupted = true
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func loadItemSRT(service *Service, item *Item) (string, error) {
	if item.Path == "" || item.Hash == "" || !inside(service.Library, item.Path) {
		return "", nil
	}
	body, err := os.ReadFile(item.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if digest(string(body)) != item.Hash {
		return "", nil
	}
	if _, err := documents.ParseSrt(string(body)); err != nil {
		return "", nil
	}
	return string(body), nil
}

func (service *Service) acquireItemSRT(ctx context.Context, task *Task, item *Item, dependencies Dependencies) (string, error) {
	if dependencies.Acquire == nil {
		return "", fmt.Errorf("字幕获取不可用")
	}
	acquired, path, err := dependencies.Acquire(ctx, *item)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != acquired {
		return "", fmt.Errorf("字幕保存校验失败")
	}
	item.Path = path
	item.Hash = digest(acquired)
	item.SourceMethod = "站内字幕"
	item.Status = "等待处理"
	if err := service.save(*task); err != nil {
		return "", err
	}
	return acquired, nil
}

func (service *Service) syncItem(ctx context.Context, task *Task, index int, dependencies Dependencies) error {
	item := &task.Items[index]
	if item.Generation == nil && dependencies.Generation != nil {
		generation, err := dependencies.Generation(ctx, item.URL)
		if err != nil {
			return err
		}
		if generation != "" {
			item.Generation = &generation
		}
		if err := service.save(*task); err != nil {
			return err
		}
	}
	srt, err := loadItemSRT(service, item)
	if err != nil {
		return err
	}
	if srt == "" {
		srt, err = service.acquireItemSRT(ctx, task, item, dependencies)
		if err != nil {
			return err
		}
	}
	if dependencies.Publish == nil {
		return fmt.Errorf("知识库发布不可用")
	}
	unchanged, generation, err := dependencies.Publish(ctx, *item, srt, task.Category)
	if err != nil {
		return err
	}
	if generation != "" {
		item.Generation = &generation
	}
	item.Status = "已完成"
	item.Unchanged = unchanged
	item.Error = ""
	return nil
}

func (service *Service) Run(ctx context.Context, id string, dependencies Dependencies) (Task, error) {
	task, err := service.prepareSyncTask(id)
	if err != nil {
		return Task{}, err
	}
	defer func() {
		service.mu.Lock()
		_ = service.save(task)
		service.mu.Unlock()
	}()
	for index := range task.Items {
		if err := ctx.Err(); err != nil {
			return Task{}, err
		}
		item := &task.Items[index]
		if dependencies.Progress != nil {
			dependencies.Progress(fmt.Sprintf("同步知识库 %d/%d · P%d %s", index+1, len(task.Items), item.Page, item.Title))
		}
		if err := service.syncItem(ctx, &task, index, dependencies); err != nil {
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
