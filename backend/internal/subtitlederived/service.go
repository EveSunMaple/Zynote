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
	"sync"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/generationjob"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/subtitlesync"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
)

type Source struct {
	Kind       string `json:"kind,omitempty"`
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
	Anchor    *CueAnchor `json:"anchor,omitempty"`
	Cue       float64    `json:"cue"`
	Quote     string     `json:"quote"`
	Point     string     `json:"point"`
	Topic     string     `json:"topic"`
	Timestamp string     `json:"timestamp"`
}

type Prepared struct {
	Evidence *contract.SummaryEvidence `json:"evidence,omitempty"`
	Markdown string                    `json:"markdown"`
	Points   []Point                   `json:"points"`
}

type Item struct {
	SourceBinding          *contract.PlaybackSourceBinding `json:"sourceBinding,omitempty"`
	SourceBindingSHA256    string                          `json:"sourceBindingSHA256,omitempty"`
	BindingAuthoritySHA256 string                          `json:"bindingAuthoritySHA256,omitempty"`
	Source                 Source                          `json:"source"`
	Mode                   string                          `json:"mode"`
	Status                 string                          `json:"status"`
	Error                  string                          `json:"error,omitempty"`
	Outputs                []Output                        `json:"outputs,omitempty"`
	Prepared               *Prepared                       `json:"prepared,omitempty"`
	GenerationState        string                          `json:"generationState,omitempty"`
	Recipe                 *ItemRecipe                     `json:"recipe,omitempty"`
	AttemptEvidence        *AttemptEvidenceRef             `json:"attemptEvidence,omitempty"`
	Received               *ReceivedSummary                `json:"received,omitempty"`
	SummaryEvidence        *contract.SummaryEvidence       `json:"summaryEvidence,omitempty"`
	PublicationPlan        *PublicationRef                 `json:"publicationPlan,omitempty"`
	PublicationReceipt     *PublicationRef                 `json:"publicationReceipt,omitempty"`
	PublicationRecord      *PublicationRecord              `json:"publicationRecord,omitempty"`
}

type Task struct {
	ExecutionGeneration int64       `json:"executionGeneration,omitempty"`
	CacheEpoch          int64       `json:"cacheEpoch,omitempty"`
	OutputVersion       int         `json:"outputVersion,omitempty"`
	ID                  string      `json:"id"`
	CreatedAt           string      `json:"createdAt"`
	CourseName          string      `json:"courseName,omitempty"`
	Interrupted         bool        `json:"interrupted"`
	Items               []Item      `json:"items"`
	QueueState          string      `json:"queueState,omitempty"`
	RequestHash         string      `json:"requestHash,omitempty"`
	EvidenceVersion     int         `json:"evidenceVersion,omitempty"`
	Recipe              *TaskRecipe `json:"recipe,omitempty"`
}

type Dependencies struct {
	SummarizeWithEvidence func(ctx context.Context, srt, mode string) (SummaryResult, error)
	Generation            func(ctx context.Context, url string) (string, error)
	ValidateModel         func(ctx context.Context) error
	Summarize             func(ctx context.Context, srt, mode string) (string, []Point, error)
	RenderSVG             func(title string, points []Point) string
	Progress              func(message string)
	ResolveRecipes        func(context.Context, []string) (contract.RequestedGenerationConfig, map[string]ModeRecipe, error)
}

type Service struct {
	Root               string
	Library            string
	publicationHook    func(string) error
	auditTraversalHook func()
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
	return service.withTaskFence(context.Background(), task, func() error { return service.saveUnchecked(task) })
}

func (service *Service) withTaskFence(ctx context.Context, task Task, publish func() error) error {
	if task.ExecutionGeneration > 0 {
		ledger, err := generationjob.Open(filepath.Join(service.Root, "job-state"))
		if err != nil {
			return err
		}
		defer ledger.Close()
		return ledger.WithFence(ctx, generationjob.Fence{JobID: task.ID, Generation: task.ExecutionGeneration, Epoch: task.CacheEpoch}, publish)
	}
	return publish()
}

func (service *Service) saveUnchecked(task Task) error {
	if err := os.MkdirAll(service.Root, 0o755); err != nil {
		return fmt.Errorf("笔记任务目录不可用")
	}
	encoded, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(service.Root, task.ID+".json")
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("笔记任务保存失败")
	}
	_, writeErr := file.Write(encoded)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
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
	if err := service.validateTaskBinding(task); err != nil {
		return Task{}, err
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
	return service.CreateOperation(sources, modes, courseName, ids.New())
}

// CreateDirectOperation snapshots supplied subtitles before any model dispatch.
func (service *Service) CreateDirectOperation(srt, title, mode, course, id string) (Task, error) {
	return service.createDirectOperation(srt, title, mode, course, id, nil)
}

func (service *Service) CreateRetainedOperation(ctx context.Context, verified videoppt.VerifiedRetainedSubtitle, mode, course, id string) (Task, error) {
	binding, srt, title, err := verified.ForCreation(ctx, service.Library, course)
	if err != nil {
		return Task{}, err
	}
	return service.createDirectOperation(srt, title, mode, course, id, &binding)
}

func (service *Service) createDirectOperation(srt, title, mode, course, id string, binding *contract.PlaybackSourceBinding) (Task, error) {
	if !validUUID(id) || !validMode(mode) || title == "" || len([]rune(title)) > 200 || len(srt) == 0 || len(srt) > 4000000 {
		return Task{}, fmt.Errorf("摘要参数无效")
	}
	if _, err := documents.ParseSrt(srt); err != nil {
		return Task{}, err
	}
	directory := filepath.Join(service.Root, "inputs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Task{}, err
	}
	path := filepath.Join(directory, id+".srt")
	// Publish a fully synced file without overwriting a concurrent/original input.
	temporary, err := os.CreateTemp(directory, ".srt-*")
	if err != nil {
		return Task{}, err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.WriteString(srt)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return Task{}, fmt.Errorf("字幕快照保存失败")
	}
	if err := os.Link(temporary.Name(), path); err != nil {
		previous, readErr := os.ReadFile(path)
		if readErr != nil || string(previous) != srt {
			return Task{}, fmt.Errorf("同一提交标识不能更换字幕；原任务保留")
		}
	}
	hash := digest(srt)
	return service.createOperation([]Source{{Kind: sourceDirectSRT, Title: title, Path: path, Hash: hash, Generation: "srt-sha256:" + hash}}, []string{mode}, course, id, binding)
}

var operationCreateMu sync.Mutex

// CreateOperation binds a caller's stable operation ID to immutable validated
// inputs. Repeating the same submission returns its existing checkpoints.
// The application owns one backend process per library; this mutex does not
// substitute for a distributed/multi-process lock.
func (service *Service) CreateOperation(sources []Source, modes []string, courseName, operationID string) (Task, error) {
	return service.createOperation(sources, modes, courseName, operationID, nil)
}

func (service *Service) createOperation(sources []Source, modes []string, courseName, operationID string, binding *contract.PlaybackSourceBinding) (Task, error) {
	if !validUUID(operationID) {
		return Task{}, fmt.Errorf("笔记任务标识无效")
	}
	if len(sources) < 1 || len(sources) > 1000 || len(modes) < 1 || len(modes) > 4 {
		return Task{}, fmt.Errorf("不要重复选择分集或笔记形式")
	}
	checked, err := service.validateSources(sources)
	if err != nil {
		return Task{}, err
	}
	if err := validateModes(modes); err != nil {
		return Task{}, err
	}
	request, err := bindingRequest(checked, modes, courseName, binding)
	if err != nil {
		return Task{}, err
	}
	requestHash := digest(string(request))
	operationCreateMu.Lock()
	defer operationCreateMu.Unlock()
	if binding == nil {
		if _, err := os.Lstat(filepath.Join(service.Root, operationID+".playback-binding.json")); !os.IsNotExist(err) {
			return Task{}, fmt.Errorf("原播放绑定身份不可通过未绑定入口更换")
		}
	}
	if _, err := os.Stat(filepath.Join(service.Root, operationID+".json")); err == nil {
		existing, err := service.Read(operationID)
		if err != nil || existing.RequestHash != requestHash {
			return Task{}, fmt.Errorf("同一提交标识不能更换字幕、课程或笔记形式；原任务保留")
		}
		return existing, nil
	} else if !os.IsNotExist(err) {
		return Task{}, err
	}
	task := Task{
		OutputVersion: 2,
		ID:            operationID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), RequestHash: requestHash,
		CourseName: courseName, Interrupted: true,
	}
	for _, source := range checked {
		for _, mode := range modes {
			task.Items = append(task.Items, Item{Source: source, Mode: mode, Status: "等待处理"})
		}
	}
	if binding != nil {
		if len(task.Items) != 1 || !videoppt.ValidPlaybackBinding(*binding) || task.Items[0].Source.Hash != binding.SubtitleSHA256 {
			return Task{}, fmt.Errorf("播放绑定身份无效")
		}
		bindingCopy := *binding
		task.Items[0].SourceBinding = &bindingCopy
		task.Items[0].SourceBindingSHA256 = videoppt.PlaybackBindingSHA256(bindingCopy)
		if err := service.createBindingAuthority(&task); err != nil {
			return Task{}, err
		}
	}
	if err := service.save(task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (service *Service) validateSources(sources []Source) ([]Source, error) {
	seen := map[string]bool{}
	checked := []Source{}
	for _, source := range sources {
		locator, err := subtitlesync.Locator(source.URL)
		if source.Kind == sourceDirectSRT {
			locator, err = source.Path, nil
			if source.URL != "" || !inside(filepath.Join(service.Root, "inputs"), source.Path) || source.Generation != "srt-sha256:"+source.Hash {
				return nil, fmt.Errorf("直接字幕来源身份无效")
			}
		} else if source.Kind != "" {
			return nil, fmt.Errorf("字幕来源类型无效")
		}
		if err != nil || source.Title == "" || len([]rune(source.Title)) > 200 ||
			!inside(service.Library, source.Path) || len(source.Hash) != 64 || source.Generation == "" {
			return nil, fmt.Errorf("不要重复选择分集或笔记形式")
		}
		if seen[locator] {
			return nil, fmt.Errorf("不要重复选择分集或笔记形式")
		}
		seen[locator] = true
		if source.Kind != sourceDirectSRT {
			source.URL = locator
		}
		checked = append(checked, source)
	}
	return checked, nil
}

func validateModes(modes []string) error {
	modeSeen := map[string]bool{}
	for _, mode := range modes {
		if !validMode(mode) || modeSeen[mode] {
			return fmt.Errorf("不要重复选择分集或笔记形式")
		}
		modeSeen[mode] = true
	}
	return nil
}

func (service *Service) current(source Source, dependencies Dependencies, ctx context.Context) (string, error) {
	if _, err := service.validateSources([]Source{source}); err != nil {
		return "", err
	}
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
	if source.Kind != sourceDirectSRT && dependencies.Generation != nil {
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

type derivedFile struct {
	Path string
	Text string
}

func (service *Service) validDerivedOutputs(item *Item) error {
	for _, output := range item.Outputs {
		if !inside(service.Library, output.Path) {
			return fmt.Errorf("笔记来源或成果不在课程资料目录")
		}
		body, err := os.ReadFile(output.Path)
		if err != nil || digest(string(body)) != output.Hash {
			return fmt.Errorf("已保存笔记被修改或缺失，请核对原文件；未自动覆盖")
		}
	}
	return nil
}

func (service *Service) ensurePrepared(ctx context.Context, task *Task, item *Item, dependencies Dependencies, srt string) error {
	if item.Prepared != nil {
		if item.Prepared.Evidence != nil {
			return service.verifyPointEvidence(ctx, task, item, item.Prepared.Points, item.Prepared.Evidence)
		}
		return nil
	}
	if item.Received != nil {
		return service.prepareReceived(ctx, task, item)
	}
	if dependencies.Summarize == nil && dependencies.SummarizeWithEvidence == nil {
		return fmt.Errorf("摘要不可用")
	}
	if item.GenerationState != "" && !resumableSpanItem(*item) {
		return fmt.Errorf("上次模型调用结果未知，未自动重试；请先核对服务端记录")
	}
	if dependencies.ValidateModel != nil {
		if err := dependencies.ValidateModel(ctx); err != nil {
			return err
		}
	}
	if err := service.bindRecipe(ctx, task, dependencies); err != nil {
		return err
	}
	observedContext, err := service.observationContext(ctx, task, item, srt)
	if err != nil {
		return err
	}
	if item.SourceBinding != nil {
		checkContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := videoppt.New(filepath.Join(service.Library, "VideoTasks")).ValidateRetainedPlaybackBinding(checkContext, *item.SourceBinding, task.CourseName)
		cancel()
		if err != nil {
			return err
		}
	}
	item.GenerationState = "dispatched"
	if err := service.save(*task); err != nil {
		return err
	}
	if dependencies.SummarizeWithEvidence != nil {
		result, err := dependencies.SummarizeWithEvidence(observedContext, srt, item.Mode)
		if err != nil {
			return err
		}
		return service.saveReceived(ctx, task, item, result)
	}
	markdown, points, err := dependencies.Summarize(observedContext, srt, item.Mode)
	if err != nil {
		return err
	}
	item.Prepared = &Prepared{Markdown: markdown, Points: points}
	item.GenerationState = "prepared"
	// Persist the received result before further validation/export so recovery
	// never repeats a paid call merely because a later local step failed.
	if err := service.save(*task); err != nil {
		return err
	}
	if _, err := service.current(item.Source, dependencies, ctx); err != nil {
		return err
	}
	return service.save(*task)
}

func writeDerivedFiles(files []derivedFile, created *[]string) ([]Output, error) {
	outputs := []Output{}
	for _, file := range files {
		existing, err := os.ReadFile(file.Path)
		if err == nil {
			if string(existing) != file.Text {
				return nil, fmt.Errorf("同名成果已有不同内容，未覆盖，请检查任务记录")
			}
		} else if os.IsNotExist(err) {
			temporary, err := os.CreateTemp(filepath.Dir(file.Path), ".note-*")
			if err != nil {
				return nil, err
			}
			_, writeErr := temporary.WriteString(file.Text)
			syncErr := temporary.Sync()
			closeErr := temporary.Close()
			if writeErr != nil || syncErr != nil || closeErr != nil {
				_ = os.Remove(temporary.Name())
				return nil, fmt.Errorf("笔记临时文件保存失败")
			}
			linkErr := os.Link(temporary.Name(), file.Path)
			_ = os.Remove(temporary.Name())
			if linkErr != nil {
				existing, readErr := os.ReadFile(file.Path)
				if readErr != nil || string(existing) != file.Text {
					return nil, fmt.Errorf("同名成果已有不同内容，未覆盖，请检查任务记录")
				}
			} else {
				*created = append(*created, file.Path)
			}
		} else {
			return nil, err
		}
		body, err := os.ReadFile(file.Path)
		if err != nil || string(body) != file.Text {
			return nil, fmt.Errorf("笔记保存校验失败")
		}
		outputs = append(outputs, Output{Path: file.Path, Hash: digest(file.Text)})
	}
	return outputs, nil
}

func (service *Service) processDerivedItem(ctx context.Context, task *Task, index int, dependencies Dependencies, created *[]string) error {
	item := &task.Items[index]
	srt, err := service.current(item.Source, dependencies, ctx)
	if err != nil {
		if item.PublicationPlan != nil || item.PublicationReceipt != nil || (item.Prepared != nil && item.Prepared.Evidence != nil) {
			return publicationError()
		}
		return err
	}
	if len(item.Outputs) > 0 {
		if item.PublicationReceipt != nil || item.PublicationPlan != nil {
			return service.restorePublication(ctx, task, index, srt)
		}
		if err := service.validDerivedOutputs(item); err != nil {
			return err
		}
		item.Status = "已完成"
		item.Error = ""
		return nil
	}
	if err := service.ensurePrepared(ctx, task, item, dependencies, srt); err != nil {
		return err
	}
	if item.Prepared.Evidence != nil || item.PublicationPlan != nil {
		return service.publishSummary(ctx, task, index, srt, dependencies)
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
	sourceLabel := item.Source.URL
	if item.Source.Kind == sourceDirectSRT {
		sourceLabel = "传入 SRT 快照：" + item.Source.Path
	}
	anchorText := ""
	if task.OutputVersion == 2 {
		points, text, err := anchorPoints(srt, item.Prepared.Points)
		if err != nil {
			return err
		}
		anchorText = text
		item.Prepared.Points = points
		if err := service.save(*task); err != nil {
			return err
		}
	}
	provenance := "\n\n---\n字幕来源：" + sourceLabel + "\n来源版本：" + item.Source.Generation +
		"\n字幕 SHA256：" + item.Source.Hash + "\n"
	files := []derivedFile{
		{Path: filepath.Join(directory, stem+".md"), Text: item.Prepared.Markdown + anchorText + provenance},
		{Path: filepath.Join(directory, stem+".json"), Text: prettyJSON(item.Prepared.Points)},
	}
	if item.Mode == "mindmap" && dependencies.RenderSVG != nil {
		files = append(files, derivedFile{Path: filepath.Join(directory, stem+".svg"), Text: dependencies.RenderSVG(item.Source.Title, item.Prepared.Points)})
	}
	var outputs []Output
	err = service.withTaskFence(ctx, *task, func() error {
		var writeErr error
		outputs, writeErr = writeDerivedFiles(files, created)
		return writeErr
	})
	if err != nil {
		return err
	}
	if _, err := service.current(item.Source, dependencies, ctx); err != nil {
		return err
	}
	item.Outputs = outputs
	item.Status = "已完成"
	item.Error = ""
	item.Prepared = nil
	return nil
}

func (service *Service) Run(ctx context.Context, id string, dependencies Dependencies) (Task, error) {
	task, err := service.Read(id)
	if err != nil {
		return Task{}, err
	}
	release, err := generationjob.AcquireRunLock(filepath.Join(service.Root, "job-state"), id)
	if err != nil {
		return Task{}, err
	}
	defer release()
	task, err = service.Read(id)
	if err != nil {
		return Task{}, err
	}
	if task.OutputVersion != 0 && task.OutputVersion != 2 {
		return Task{}, fmt.Errorf("笔记成果版本不受支持，原任务保留")
	}
	needsProvider := false
	for _, item := range task.Items {
		if item.Prepared == nil && item.Received == nil && len(item.Outputs) == 0 && item.GenerationState == "" {
			needsProvider = true
		}
	}
	if needsProvider || task.ExecutionGeneration > 0 {
		ledger, err := generationjob.Open(filepath.Join(service.Root, "job-state"))
		if err != nil {
			return Task{}, err
		}
		defer ledger.Close()
		// Policy v1: a finite engineering request-count cap, not a token or
		// monetary budget. Large jobs stop before exceeding it. The persisted
		// cap is shared by all modes, chunks, retries and repairs, across resume.
		const physicalAttemptLimit int64 = 256
		// SQLite is the generation authority. Re-read the durable manifest after
		// CAS so a crash between CAS and JSON publication is recoverable, without
		// losing a prefix saved just before rollover.
		expected, err := ledger.CurrentGeneration(ctx, task.ID)
		if err != nil {
			return Task{}, err
		}
		fence, err := ledger.Acquire(ctx, task.ID, expected, physicalAttemptLimit)
		if err != nil {
			return Task{}, err
		}
		task, err = service.Read(id)
		if err != nil {
			return Task{}, err
		}
		task.ExecutionGeneration, task.CacheEpoch = fence.Generation, fence.Epoch
		ctx = contract.WithPhysicalRequestGuard(ctx, generationjob.Guard{Store: ledger, Fence: fence})
		ctx = contract.WithSuccessCache(ctx, generationjob.ScopedCache{Store: ledger, Fence: fence})
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
		if err := service.processDerivedItem(ctx, &task, index, dependencies, &created); err != nil {
			_ = service.withTaskFence(context.WithoutCancel(ctx), task, func() error {
				for _, path := range created {
					_ = os.Remove(path)
				}
				return nil
			})
			if ctx.Err() != nil {
				return Task{}, ctx.Err()
			}
			item.Status = "失败"
			item.Error = err.Error()
			if contract.IsObservationFailure(err) {
				if saveErr := service.save(task); saveErr != nil {
					return task, saveErr
				}
				return task, err
			}
			if item.GenerationState != "" && item.Prepared == nil && len(item.Outputs) == 0 {
				if saveErr := service.save(task); saveErr != nil {
					return task, saveErr
				}
				return task, nil
			}
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

const sourceDirectSRT = "direct-srt"
