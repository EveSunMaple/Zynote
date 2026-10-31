package videoppt

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/documents"
)

// StationSubtitle is an allowlisted content checkpoint, not a video readiness
// proof. An explicit no-subtitle decision is bound to the same URL/page too.
type StationSubtitle struct {
	Version int    `json:"version"`
	URL     string `json:"url"`
	Page    int    `json:"page"`
	State   string `json:"state"`
	Title   string `json:"title,omitempty"`
	Source  string `json:"source,omitempty"`
	Warning string `json:"warning,omitempty"`
	SRT     string `json:"srt,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Count   int    `json:"count,omitempty"`
}

func validateStationSubtitle(operation VideoOperation, record StationSubtitle) error {
	source, err := operationSource(operation)
	if err != nil || operation.Stage != "subtitle-station" || source.Kind != "bili" || operation.Page != source.Page || record.Version != 1 || record.URL != source.URL || record.Page != source.Page {
		return requestError("站内字幕记录与任务来源不一致")
	}
	if record.State == "no-subtitle" {
		if record.SRT != "" || record.SHA256 != "" || record.Count != 0 || record.Title != "" || record.Source != "" || record.Warning != "" {
			return requestError("无字幕记录无效")
		}
		return nil
	}
	if record.State != "prepared" || record.SRT == "" || record.Count < 1 {
		return requestError("站内字幕内容记录无效")
	}
	sum := sha256.Sum256([]byte(record.SRT))
	segments, err := documents.ParseSrt(record.SRT)
	if err != nil || len(segments) != record.Count || record.SHA256 != hex.EncodeToString(sum[:]) {
		return requestError("站内字幕内容与摘要不一致")
	}
	return nil
}

func (service *Service) SaveStationSubtitle(task CreatedTask, record StationSubtitle) error {
	operation, err := service.Operation(task.ID)
	if err != nil {
		return err
	}
	source, err := operationSource(operation)
	if err != nil {
		return err
	}
	record.Version = 1
	record.URL = source.URL
	record.Page = source.Page
	if record.State == "prepared" {
		sum := sha256.Sum256([]byte(record.SRT))
		record.SHA256 = hex.EncodeToString(sum[:])
	}
	if err := validateStationSubtitle(operation, record); err != nil {
		return err
	}
	return saveTaskJSON(filepath.Join(task.Dir, "station-subtitle.json"), record)
}

func (service *Service) LoadStationSubtitle(task CreatedTask) (StationSubtitle, bool, error) {
	var record StationSubtitle
	body, err := os.ReadFile(filepath.Join(task.Dir, "station-subtitle.json"))
	if os.IsNotExist(err) {
		return record, false, nil
	}
	if err != nil {
		return record, false, err
	}
	operation, err := service.Operation(task.ID)
	if err != nil {
		return record, true, err
	}
	if json.Unmarshal(body, &record) != nil {
		return record, true, requestError("站内字幕记录无效")
	}
	return record, true, validateStationSubtitle(operation, record)
}
