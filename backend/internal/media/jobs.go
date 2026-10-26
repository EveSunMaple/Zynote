package media

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const JobIDPattern = `^[a-f0-9]{32}$`

var jobIDRegexp = regexp.MustCompile(JobIDPattern)
var trackLockPattern = regexp.MustCompile(`^track-\d+(?:-segment-\d+)?(?:-chunk-\d+)?\.lock$`)

type Receipt struct {
	Relative string `json:"relative"`
	Bytes    int64  `json:"bytes"`
	Hash     string `json:"hash"`
}

type JobProgress struct {
	Downloaded *float64 `json:"downloaded,omitempty"`
	Total      *float64 `json:"total,omitempty"`
}

type JobLegacy struct {
	ID         string `json:"id"`
	RecordHash string `json:"recordHash"`
}

type NativeDownloadJob struct {
	Version      int            `json:"version"`
	ID           string         `json:"id"`
	Legacy       *JobLegacy     `json:"legacy,omitempty"`
	Revision     int            `json:"revision"`
	Identity     string         `json:"identity"`
	SourceURL    string         `json:"source_url"`
	Title        string         `json:"title"`
	Recipe       map[string]any `json:"recipe"`
	RecipeHash   string         `json:"recipeHash"`
	State        string         `json:"state"`
	Stage        string         `json:"stage"`
	Progress     JobProgress    `json:"progress"`
	ErrorMessage string         `json:"error_message"`
	Warning      string         `json:"warning"`
	Created      string         `json:"created"`
	Updated      string         `json:"updated"`
	Outputs      []Receipt      `json:"outputs"`
	Cleanup      []string       `json:"cleanup"`
}

type Crypt func(data []byte, decrypt bool) ([]byte, error)

var allowedTransitions = map[string][]string{
	"queued":      {"running", "cancelled"},
	"running":     {"running", "cancelled", "interrupted", "error"},
	"interrupted": {"queued"},
	"cancelled":   {"queued"},
	"error":       {"queued"},
	"completed":   {},
}

var reservedRecordNames = map[string]bool{
	"job.json": true, "job.writing": true, "job.dpapi": true, "job.writing.dpapi": true,
}

func hashRecipe(identity, source string, recipe any) string {
	encoded, _ := json.Marshal([]any{identity, source, recipe})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

type DownloadJobs struct {
	root        string
	crypt       Crypt
	mu          chan struct{}
	records     map[string]*NativeDownloadJob
	initialized bool
}

func NewDownloadJobs(root string, crypt Crypt) *DownloadJobs {
	if crypt == nil {
		crypt = func(data []byte, decrypt bool) ([]byte, error) { return data, nil }
	}
	if err := os.MkdirAll(root, 0o755); err == nil {
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
	}
	return &DownloadJobs{root: root, crypt: crypt, mu: make(chan struct{}, 1), records: map[string]*NativeDownloadJob{}}
}

func (jobs *DownloadJobs) exclusive(run func() error) error {
	jobs.mu <- struct{}{}
	defer func() { <-jobs.mu }()
	return run()
}

func (jobs *DownloadJobs) validateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("下载记录目录无效")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("下载记录目录无效")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || strings.ToLower(real) != strings.ToLower(path) {
		return fmt.Errorf("下载记录目录无效")
	}
	return nil
}

func (jobs *DownloadJobs) base() (string, error) {
	base := filepath.Join(jobs.root, "native-downloads")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("下载记录目录无效")
	}
	if err := jobs.validateDirectory(base); err != nil {
		return "", err
	}
	return base, nil
}

func (jobs *DownloadJobs) directory(id string) (string, error) {
	if !jobIDRegexp.MatchString(id) {
		return "", fmt.Errorf("下载任务标识无效")
	}
	base, err := jobs.base()
	if err != nil {
		return "", err
	}
	path := filepath.Join(base, id)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", fmt.Errorf("下载任务目录无效")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("下载任务目录无效")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || strings.ToLower(real) != strings.ToLower(path) {
		return "", fmt.Errorf("下载任务目录无效")
	}
	return path, nil
}

func (jobs *DownloadJobs) save(job *NativeDownloadJob) error {
	directory, err := jobs.directory(job.ID)
	if err != nil {
		return err
	}
	encrypted := job.Recipe["privacy"] == true
	name := "job.json"
	if encrypted {
		name = "job.dpapi"
	}
	writingName := "job.writing"
	if encrypted {
		writingName = "job.writing.dpapi"
	}
	path := filepath.Join(directory, name)
	tmp := filepath.Join(directory, writingName)
	for _, target := range []string{path, tmp} {
		info, err := os.Lstat(target)
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			return fmt.Errorf("下载记录文件无效")
		}
	}
	serialized, _ := json.Marshal(job)
	if encrypted {
		serialized, err = jobs.crypt(serialized, false)
		if err != nil {
			return err
		}
	}
	if err := os.WriteFile(tmp, serialized, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	jobs.records[job.ID] = job
	return nil
}

func (jobs *DownloadJobs) Initialize() error {
	jobs.mu <- struct{}{}
	defer func() { <-jobs.mu }()
	return jobs.ensureInitializedLocked()
}

func (jobs *DownloadJobs) ensureInitializedLocked() error {
	if jobs.initialized {
		return nil
	}
	if err := jobs.initializeLocked(); err != nil {
		return err
	}
	jobs.initialized = true
	return nil
}

func (jobs *DownloadJobs) initializeLocked() error {
	base, err := jobs.base()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return fmt.Errorf("下载记录目录无效")
	}
	for _, entry := range entries {
		if !jobIDRegexp.MatchString(entry.Name()) || !entry.IsDir() {
			continue
		}
		if info, err := entry.Info(); err == nil && info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		directory, err := jobs.directory(entry.Name())
		if err != nil {
			return err
		}
		privatePath := filepath.Join(directory, "job.dpapi")
		publicPath := filepath.Join(directory, "job.json")
		encrypted := false
		if _, err := os.Lstat(privatePath); err == nil {
			encrypted = true
		}
		if encrypted {
			if _, err := os.Lstat(publicPath); err == nil {
				return fmt.Errorf("下载任务同时存在明文与加密记录，未选择其中一个覆盖")
			}
		}
		recordPath := publicPath
		if encrypted {
			recordPath = privatePath
		}
		info, err := os.Lstat(recordPath)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("下载恢复记录文件无效")
		}
		raw, err := os.ReadFile(recordPath)
		if err != nil {
			return fmt.Errorf("下载恢复记录文件无效")
		}
		if encrypted {
			raw, err = jobs.crypt(raw, true)
			if err != nil {
				return err
			}
		}
		var row NativeDownloadJob
		if err := json.Unmarshal(raw, &row); err != nil {
			return fmt.Errorf("下载恢复记录无效")
		}
		recipe, err := MediaOptions(row.Recipe)
		if err != nil {
			return err
		}
		row.Recipe = recipeMap(recipe)
		if encrypted != (row.Recipe["privacy"] == true) {
			return fmt.Errorf("下载记录加密方式与任务设置不一致")
		}
		if row.ID != entry.Name() || row.RecipeHash != hashRecipe(row.Identity, row.SourceURL, row.Recipe) {
			return fmt.Errorf("下载恢复记录与任务不一致")
		}
		locks, err := RecoverDownloadLocks(directory, row.Identity)
		if err != nil {
			return err
		}
		if len(locks.Preserved) > 0 {
			row.Warning = "保留无法确认归属的分段锁，请核对原下载进程后再继续"
		}
		if row.State == "running" || row.State == "queued" {
			row.Revision++
			row.State = "interrupted"
			row.Stage = "上次中断，等待手动继续"
			row.Updated = time.Now().UTC().Format(time.RFC3339Nano)
			if err := jobs.save(&row); err != nil {
				return err
			}
		} else {
			jobs.records[row.ID] = &row
			if row.State == "completed" && len(row.Cleanup) > 0 {
				if _, err := jobs.clearIntermediatesLocked(&row); err != nil {
					row.Warning = "成果或清理记录未通过核对，原中间文件保留"
					jobs.records[row.ID] = &row
				}
			}
		}
	}
	return nil
}

func OptionsMap(options NativeMediaOptions) map[string]any { return recipeMap(options) }

func recipeMap(options NativeMediaOptions) map[string]any {
	encoded, _ := json.Marshal(options)
	result := map[string]any{}
	_ = json.Unmarshal(encoded, &result)
	return result
}

func (jobs *DownloadJobs) Create(identity, url, title string, options any, legacy *JobLegacy) (*NativeDownloadJob, error) {
	jobs.mu <- struct{}{}
	defer func() { <-jobs.mu }()
	if err := jobs.ensureInitializedLocked(); err != nil {
		return nil, err
	}
	recipe, err := MediaOptions(options)
	if err != nil {
		return nil, err
	}
	if legacy != nil && jobs.records[legacy.ID] != nil {
		return nil, fmt.Errorf("此旧任务已经迁入原生队列，请刷新记录")
	}
	if strings.TrimSpace(identity) == "" || utf8.RuneCountInString(identity) > 500 {
		return nil, fmt.Errorf("下载任务标识无效")
	}
	id := ""
	if legacy != nil {
		id = legacy.ID
	} else {
		id = strings.ReplaceAll(fmt.Sprintf("%s", uuidV4()), "-", "")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if len([]rune(title)) > 240 {
		title = string([]rune(title)[:240])
	}
	row := &NativeDownloadJob{
		Version: 1, ID: id, Legacy: legacy, Revision: 0, Identity: identity,
		SourceURL: url, Title: title, Recipe: recipeMap(recipe),
		RecipeHash: hashRecipe(identity, url, recipeMap(recipe)),
		State:      "queued", Stage: "等待下载", Created: now, Updated: now,
		Outputs: []Receipt{}, Cleanup: []string{},
	}
	if err := jobs.save(row); err != nil {
		return nil, err
	}
	return row, nil
}

func (jobs *DownloadJobs) List() ([]*NativeDownloadJob, error) {
	jobs.mu <- struct{}{}
	defer func() { <-jobs.mu }()
	result := []*NativeDownloadJob{}
	for _, row := range jobs.records {
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Created > result[j].Created
	})
	return result, nil
}

func (jobs *DownloadJobs) Read(id string) (*NativeDownloadJob, error) {
	if !jobIDRegexp.MatchString(id) {
		return nil, fmt.Errorf("下载任务标识无效")
	}
	jobs.mu <- struct{}{}
	defer func() { <-jobs.mu }()
	row := jobs.records[id]
	if row == nil {
		return nil, fmt.Errorf("下载任务不存在")
	}
	return row, nil
}

func (jobs *DownloadJobs) Transition(id string, revision int, state string, stage, errorMessage, warning string, progress *JobProgress) (*NativeDownloadJob, error) {
	jobs.mu <- struct{}{}
	defer func() { <-jobs.mu }()
	current := jobs.records[id]
	if current == nil {
		return nil, fmt.Errorf("下载任务不存在")
	}
	if current.Revision != revision {
		return nil, fmt.Errorf("下载记录已改变，请刷新后继续")
	}
	allowed, ok := allowedTransitions[current.State]
	if !ok || !inList(state, allowed) {
		return nil, fmt.Errorf("下载状态转换无效")
	}
	next := *current
	next.Revision = revision + 1
	next.State = state
	if stage != "" {
		next.Stage = stage
	}
	next.ErrorMessage = errorMessage
	if warning != "" {
		next.Warning = warning
	}
	if progress != nil {
		next.Progress = *progress
	}
	next.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	if err := jobs.save(&next); err != nil {
		return nil, err
	}
	return &next, nil
}

func (jobs *DownloadJobs) ownedFile(id, name string) (string, os.FileInfo, error) {
	directory, err := jobs.directory(id)
	if err != nil {
		return "", nil, err
	}
	if filepath.IsAbs(name) || strings.Contains(name, "..") ||
		strings.ContainsAny(name, ":") {
		return "", nil, fmt.Errorf("成果路径不属于下载任务")
	}
	path := filepath.Join(directory, name)
	part, err := filepath.Rel(directory, path)
	if err != nil || part == "" || part == ".." || strings.HasPrefix(part, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(part) {
		return "", nil, fmt.Errorf("成果路径不属于下载任务")
	}
	for _, piece := range strings.FieldsFunc(part, func(r rune) bool { return r == '/' || r == '\\' }) {
		if reservedRecordNames[strings.ToLower(piece)] {
			return "", nil, fmt.Errorf("成果路径不属于下载任务")
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("下载成果文件不可用")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || strings.ToLower(real) != strings.ToLower(path) {
		return "", nil, fmt.Errorf("下载成果文件不可用")
	}
	return path, info, nil
}

func (jobs *DownloadJobs) Evidence(id, name string) (Receipt, error) {
	path, info, err := jobs.ownedFile(id, name)
	if err != nil {
		return Receipt{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, fmt.Errorf("下载成果文件不可用")
	}
	return Receipt{Relative: name, Bytes: info.Size(), Hash: ComputeHash(raw)}, nil
}

func (jobs *DownloadJobs) Complete(id string, revision int, outputs []Receipt, cleanup []string) (*NativeDownloadJob, error) {
	jobs.mu <- struct{}{}
	defer func() { <-jobs.mu }()
	row := jobs.records[id]
	if row == nil || row.Revision != revision || row.State != "running" {
		return nil, fmt.Errorf("任务已取消、改变或不在运行，拒绝发布旧成果")
	}
	if len(outputs) == 0 || len(outputs) != uniqueLowerCount(outputs) {
		return nil, fmt.Errorf("成果清单为空或重复")
	}
	for _, expected := range outputs {
		actual, err := jobs.Evidence(id, expected.Relative)
		if err != nil {
			return nil, err
		}
		if actual.Bytes != expected.Bytes || actual.Hash != expected.Hash {
			return nil, fmt.Errorf("下载成果校验失败，保留恢复文件")
		}
	}
	names := uniqueStrings(cleanup)
	if err := checkCleanup(names, outputs); err != nil {
		return nil, err
	}
	next := *row
	next.Revision = revision + 1
	next.State = "completed"
	next.Stage = "成果已核验"
	next.Outputs = outputs
	next.Cleanup = names
	next.ErrorMessage = ""
	next.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	if err := jobs.save(&next); err != nil {
		return nil, err
	}
	return &next, nil
}

func checkCleanup(names []string, outputs []Receipt) error {
	for _, name := range names {
		if name == "" || regexp.MustCompile(`[\\/:]|\x00-\x1f`).MatchString(name) ||
			regexp.MustCompile(`[. ]$`).MatchString(name) || strings.TrimSpace(name) != name ||
			reservedRecordNames[strings.ToLower(name)] ||
			func() bool {
				for _, output := range outputs {
					if strings.EqualFold(output.Relative, name) {
						return true
					}
				}
				return false
			}() {
			return fmt.Errorf("清理清单包含非中间文件")
		}
	}
	return nil
}

func uniqueLowerCount(outputs []Receipt) int {
	seen := map[string]bool{}
	count := 0
	for _, output := range outputs {
		key := strings.ToLower(output.Relative)
		if !seen[key] {
			seen[key] = true
			count++
		}
	}
	return count
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func (jobs *DownloadJobs) clearIntermediatesLocked(row *NativeDownloadJob) ([]string, error) {
	if err := checkCleanup(row.Cleanup, row.Outputs); err != nil {
		return nil, err
	}
	for _, expected := range row.Outputs {
		actual, err := jobs.Evidence(row.ID, expected.Relative)
		if err != nil {
			return nil, err
		}
		if actual.Bytes != expected.Bytes || actual.Hash != expected.Hash {
			return nil, fmt.Errorf("成果已改变，原中间文件保留")
		}
	}
	remaining := []string{}
	for _, name := range row.Cleanup {
		path, _, err := jobs.ownedFile(row.ID, name)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			remaining = append(remaining, name)
		}
	}
	next := *row
	next.Revision++
	next.Cleanup = remaining
	if len(remaining) > 0 {
		warning := "部分中间文件未能清理，已保留恢复记录"
		if next.Warning != "" {
			warning = next.Warning + "；" + warning
		}
		next.Warning = warning
	}
	next.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	if err := jobs.save(&next); err != nil {
		return nil, err
	}
	*row = next
	return remaining, nil
}

func (jobs *DownloadJobs) CleanupCompleted(id string) ([]string, error) {
	jobs.mu <- struct{}{}
	defer func() { <-jobs.mu }()
	row := jobs.records[id]
	if row == nil || row.State != "completed" {
		return nil, fmt.Errorf("成果未完成，不能清理")
	}
	if len(row.Cleanup) == 0 {
		return []string{}, nil
	}
	return jobs.clearIntermediatesLocked(row)
}

func (jobs *DownloadJobs) File(id string) (string, error) {
	row, err := jobs.Read(id)
	if err != nil {
		return "", err
	}
	if row.State != "completed" || len(row.Outputs) == 0 {
		return "", fmt.Errorf("此任务尚无已核验成果")
	}
	for _, expected := range row.Outputs {
		actual, err := jobs.Evidence(id, expected.Relative)
		if err != nil {
			return "", err
		}
		if actual.Hash != expected.Hash || actual.Bytes != expected.Bytes {
			return "", fmt.Errorf("成果已改变或缺失，请重新检查")
		}
	}
	path, _, err := jobs.ownedFile(id, row.Outputs[0].Relative)
	return path, err
}

func (jobs *DownloadJobs) TaskDirectory(id string) (string, error) {
	if _, err := jobs.Read(id); err != nil {
		return "", err
	}
	return jobs.directory(id)
}

type lockOwner struct {
	Version  int    `json:"version"`
	PID      int    `json:"pid"`
	Identity string `json:"identity"`
}

type LockRecovery struct {
	Removed   []string
	Preserved []string
}

func RecoverDownloadLocks(directory, identity string) (*LockRecovery, error) {
	result := &LockRecovery{Removed: []string{}, Preserved: []string{}}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return result, nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if !trackLockPattern.MatchString(name) {
			continue
		}
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			result.Preserved = append(result.Preserved, name)
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			result.Preserved = append(result.Preserved, name)
			continue
		}
		var owner lockOwner
		if json.Unmarshal(raw, &owner) != nil || owner.Version != 1 || owner.PID <= 0 || owner.Identity == "" {
			result.Preserved = append(result.Preserved, name)
			continue
		}
		if !strings.HasPrefix(owner.Identity, identity+":") {
			result.Preserved = append(result.Preserved, name)
			continue
		}
		dead := !processAlive(owner.PID)
		if !dead {
			result.Preserved = append(result.Preserved, name)
			continue
		}
		if err := os.Remove(path); err == nil {
			result.Removed = append(result.Removed, name)
		} else {
			result.Preserved = append(result.Preserved, name)
		}
	}
	return result, nil
}

func uuidV4() string {
	return ids.New()
}
