package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

const (
	zhiyunTaskQueued      = "queued"
	zhiyunTaskRunning     = "running"
	zhiyunTaskCancelling  = "cancelling"
	zhiyunTaskCompleted   = "completed"
	zhiyunTaskFailed      = "failed"
	zhiyunTaskCancelled   = "cancelled"
	zhiyunTaskInterrupted = "interrupted"
)

var zhiyunTaskIDPattern = regexp.MustCompile(`^[0-9a-f-]{36}$`)

type zhiyunTask struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	Title          string `json:"title"`
	CourseID       string `json:"courseId"`
	SubID          string `json:"subId"`
	CourseName     string `json:"courseName"`
	LocalCourseID  string `json:"localCourseId,omitempty"`
	State          string `json:"state"`
	Stage          string `json:"stage"`
	Error          string `json:"error,omitempty"`
	Warning        string `json:"warning,omitempty"`
	OutputPath     string `json:"outputPath,omitempty"`
	LectureID      string `json:"lectureId,omitempty"`
	ResultCourseID string `json:"resultCourseId,omitempty"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type zhiyunTaskStore struct {
	root   string
	mu     sync.Mutex
	tasks  map[string]*zhiyunTask
	loaded bool
}

func newZhiyunTaskStore(root string) *zhiyunTaskStore {
	return &zhiyunTaskStore{root: root, tasks: map[string]*zhiyunTask{}}
}

func nowStamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func cloneZhiyunTask(task *zhiyunTask) *zhiyunTask {
	if task == nil {
		return nil
	}
	copy := *task
	return &copy
}

func (store *zhiyunTaskStore) loadLocked() error {
	if store.loaded {
		return nil
	}
	if err := os.MkdirAll(store.root, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(store.root, entry.Name()))
		if err != nil {
			continue
		}
		var task zhiyunTask
		if json.Unmarshal(raw, &task) != nil || !zhiyunTaskIDPattern.MatchString(task.ID) {
			continue
		}
		if task.State == zhiyunTaskQueued || task.State == zhiyunTaskRunning || task.State == zhiyunTaskCancelling {
			task.State = zhiyunTaskInterrupted
			task.Stage = "应用上次退出，任务已中断"
			task.UpdatedAt = nowStamp()
			_ = store.saveLocked(&task)
		}
		store.tasks[task.ID] = &task
	}
	store.loaded = true
	return nil
}

func (store *zhiyunTaskStore) saveLocked(task *zhiyunTask) error {
	if err := os.MkdirAll(store.root, 0o755); err != nil {
		return err
	}
	encoded, err := json.Marshal(task)
	if err != nil {
		return err
	}
	temporary := filepath.Join(store.root, task.ID+".json.tmp")
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(store.root, task.ID+".json"))
}

func (store *zhiyunTaskStore) List() ([]*zhiyunTask, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.loadLocked(); err != nil {
		return nil, err
	}
	result := make([]*zhiyunTask, 0, len(store.tasks))
	for _, task := range store.tasks {
		result = append(result, cloneZhiyunTask(task))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CreatedAt == result[right].CreatedAt {
			return result[left].ID > result[right].ID
		}
		return result[left].CreatedAt > result[right].CreatedAt
	})
	return result, nil
}

func (store *zhiyunTaskStore) Get(id string) (*zhiyunTask, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.loadLocked(); err != nil {
		return nil, err
	}
	task := store.tasks[id]
	if task == nil {
		return nil, contract.InputInvalid("智云任务不存在")
	}
	return cloneZhiyunTask(task), nil
}

func (store *zhiyunTaskStore) Create(input zhiyunExportInput, title string) (*zhiyunTask, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.loadLocked(); err != nil {
		return nil, err
	}
	stamp := nowStamp()
	task := &zhiyunTask{
		ID: ids.New(), Kind: input.Kind, Title: title,
		CourseID: input.CourseID, SubID: input.SubID, CourseName: input.CourseName,
		LocalCourseID: input.LocalCourseID, State: zhiyunTaskQueued, Stage: "排队中", CreatedAt: stamp, UpdatedAt: stamp,
	}
	if err := store.saveLocked(task); err != nil {
		return nil, err
	}
	store.tasks[task.ID] = task
	return cloneZhiyunTask(task), nil
}

func (store *zhiyunTaskStore) Update(id string, apply func(*zhiyunTask)) (*zhiyunTask, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := store.loadLocked(); err != nil {
		return nil, err
	}
	task := store.tasks[id]
	if task == nil {
		return nil, contract.InputInvalid("智云任务不存在")
	}
	apply(task)
	task.UpdatedAt = nowStamp()
	if err := store.saveLocked(task); err != nil {
		return nil, err
	}
	return cloneZhiyunTask(task), nil
}

func validZhiyunTaskID(id string) bool {
	return config.IsUUID(id) && zhiyunTaskIDPattern.MatchString(id)
}

func (inv *Invoker) zhiyunTaskStore(state *contract.State) *zhiyunTaskStore {
	root := filepath.Join(state.DataRoot, "zhiyun-tasks")
	inv.zhiyunTaskMu.Lock()
	defer inv.zhiyunTaskMu.Unlock()
	if inv.zhiyunTasks == nil || inv.zhiyunTaskDir != root {
		inv.zhiyunTasks = newZhiyunTaskStore(root)
		inv.zhiyunTaskDir = root
	}
	return inv.zhiyunTasks
}
