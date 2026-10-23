package media

import (
	"encoding/json"
	"path/filepath"
	"sync"
)

type MediaService struct {
	Jobs *DownloadJobs
	Auth *MediaAuth
	Root string

	progressMu sync.Mutex
	progress   map[string]map[string]any
}

func NewMediaService(root string, crypt Crypt) *MediaService {
	return &MediaService{Jobs: NewDownloadJobs(root, crypt), Auth: NewMediaAuth(root, crypt), Root: root}
}

func (service *MediaService) Options() (map[string]any, error) {
	defaults, err := MediaOptions(map[string]any{})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"defaults": defaults,
		"enums":    MediaEnums,
		"heights":  MediaHeights,
		"sponsors": MediaSponsors,
	}, nil
}

func (service *MediaService) Project(job *NativeDownloadJob) map[string]any {
	encoded, _ := json.Marshal(job)
	view := map[string]any{}
	_ = json.Unmarshal(encoded, &view)
	output := ""
	if len(job.Outputs) > 0 {
		output = filepath.Join(service.Root, "native-downloads", job.ID, job.Outputs[0].Relative)
	}
	files := []string{}
	for _, receipt := range job.Outputs {
		files = append(files, receipt.Relative)
	}
	view["output"] = output
	view["files"] = files
	if job.State == "running" {
		service.progressMu.Lock()
		if value, ok := service.progress[job.ID]; ok {
			view["progress"] = value
		}
		service.progressMu.Unlock()
	}
	return view
}

func (service *MediaService) SetProgress(id string, downloaded int64, total *int64) {
	value := map[string]any{"downloaded": downloaded}
	if total != nil {
		value["total"] = *total
	}
	service.progressMu.Lock()
	defer service.progressMu.Unlock()
	if service.progress == nil {
		service.progress = map[string]map[string]any{}
	}
	service.progress[id] = value
}

func (service *MediaService) ClearProgress(id string) {
	service.progressMu.Lock()
	defer service.progressMu.Unlock()
	delete(service.progress, id)
}

func (service *MediaService) List() ([]map[string]any, error) {
	if err := service.Jobs.Initialize(); err != nil {
		return nil, err
	}
	jobs, err := service.Jobs.List()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for _, job := range jobs {
		result = append(result, service.Project(job))
	}
	return result, nil
}

func (service *MediaService) Retry(id string) (map[string]any, error) {
	if err := service.Jobs.Initialize(); err != nil {
		return nil, err
	}
	job, err := service.Jobs.Read(id)
	if err != nil {
		return nil, err
	}
	updated, err := service.Jobs.Transition(id, job.Revision, "queued", "等待继续下载", "", "", nil)
	if err != nil {
		return nil, err
	}
	return service.Project(updated), nil
}

func (service *MediaService) Cancel(id string) (map[string]any, error) {
	if err := service.Jobs.Initialize(); err != nil {
		return nil, err
	}
	job, err := service.Jobs.Read(id)
	if err != nil {
		return nil, err
	}
	if job.State == "queued" {
		if _, err := service.Jobs.Transition(id, job.Revision, "cancelled", "已取消", "", "", nil); err != nil {
			return nil, err
		}
	}
	updated, err := service.Jobs.Read(id)
	if err != nil {
		return nil, err
	}
	return service.Project(updated), nil
}

func (service *MediaService) File(id string) (string, error) {
	if err := service.Jobs.Initialize(); err != nil {
		return "", err
	}
	return service.Jobs.File(id)
}

func (service *MediaService) Create(identity, url, title string, options any, legacy *JobLegacy) (map[string]any, error) {
	job, err := service.Jobs.Create(identity, url, title, options, legacy)
	if err != nil {
		return nil, err
	}
	return service.Project(job), nil
}
