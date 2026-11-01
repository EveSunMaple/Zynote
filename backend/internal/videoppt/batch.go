package videoppt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

type BatchInput struct {
	ASRLanguage string `json:"asrLanguage,omitempty"`
	Kind        string `json:"kind,omitempty"`
	URL         string `json:"url"`
	Pages       []int  `json:"pages"`
	Prompt      string `json:"prompt"`
	CourseName  string `json:"courseName"`
}

type BatchEpisode struct {
	Page  int    `json:"page"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type BatchOperation struct {
	Version   int            `json:"version"`
	ID        string         `json:"id"`
	Input     BatchInput     `json:"input"`
	Episodes  []BatchEpisode `json:"episodes,omitempty"`
	Completed bool           `json:"completed,omitempty"`
}

func (service *Service) batchPath(id string) (string, error) {
	if !operationIDPattern.MatchString(id) {
		return "", requestError("批次编号无效")
	}
	return filepath.Join(service.Root, ".batches", id+".json"), nil
}

func (service *Service) Batch(id string) (BatchOperation, error) {
	path, err := service.batchPath(id)
	if err != nil {
		return BatchOperation{}, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return BatchOperation{}, err
	}
	var batch BatchOperation
	if json.Unmarshal(body, &batch) != nil || batch.Version != 1 || batch.ID != id {
		return batch, requestError("批次记录无效，请保留任务")
	}
	if err := validateBatch(batch); err != nil {
		return batch, err
	}
	return batch, nil
}

func validateBatch(batch BatchOperation) error {
	if _, err := canonicalASRLanguage(batch.Input.ASRLanguage); err != nil {
		return err
	}
	if batch.Input.Kind != "subtitle" && batch.Input.ASRLanguage != "" {
		return requestError("非字幕批次不能设置转写语言")
	}
	parent, err := operationSource(VideoOperation{URL: batch.Input.URL})
	if (batch.Input.Kind != "" && batch.Input.Kind != "subtitle") || err != nil || batch.Input.Prompt == "" || len(batch.Input.Pages) < 1 || len(batch.Input.Pages) > 1000 {
		return requestError("批次来源记录无效")
	}
	seen := map[int]bool{}
	for _, page := range batch.Input.Pages {
		if page < 1 || page > 99999 || seen[page] {
			return requestError(errBatchEpisodeInvalid)
		}
		seen[page] = true
	}
	if len(batch.Episodes) == 0 {
		if batch.Completed {
			return requestError("批次完成记录无效")
		}
		return nil
	}
	if len(batch.Episodes) != len(batch.Input.Pages) {
		return requestError(errBatchEpisodeInvalid)
	}
	for index, episode := range batch.Episodes {
		source, err := operationSource(VideoOperation{URL: episode.URL})
		if err != nil || source.Page != batch.Input.Pages[index] || episode.Page != source.Page || source.VideoID != parent.VideoID {
			return requestError("批次分集与来源不一致")
		}
	}
	return nil
}

// BindBatch reserves immutable inputs before any catalog/network work. Atomic
// no-overwrite publication prevents concurrent requests replacing the binding.
func (service *Service) BindBatch(id string, input BatchInput) (BatchOperation, error) {
	language, err := canonicalASRLanguage(input.ASRLanguage)
	if err != nil {
		return BatchOperation{}, err
	}
	input.ASRLanguage = language
	if id == "" {
		id = ids.New()
	}
	path, err := service.batchPath(id)
	if err != nil {
		return BatchOperation{}, err
	}
	batch := BatchOperation{Version: 1, ID: id, Input: input}
	if err := validateBatch(batch); err != nil {
		return batch, err
	}
	body, err := json.Marshal(batch)
	if err != nil {
		return batch, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return batch, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".batch-*")
	if err != nil {
		return batch, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err = file.Write(body); err != nil {
		return batch, err
	}
	if err = file.Sync(); err != nil {
		return batch, err
	}
	if err = file.Close(); err != nil {
		return batch, err
	}
	if err = os.Link(file.Name(), path); err != nil && !os.IsExist(err) {
		return batch, err
	}
	existing, err := service.Batch(id)
	if err != nil {
		return batch, err
	}
	wanted, _ := json.Marshal(input)
	bound, _ := json.Marshal(existing.Input)
	if !bytes.Equal(wanted, bound) {
		return batch, requestError("批次编号已绑定其他来源或参数，请新建批次")
	}
	return existing, nil
}

// SetBatchEpisodes is called only by the batch's queued preparation owner.
func (service *Service) SetBatchEpisodes(id string, episodes []BatchEpisode) (BatchOperation, error) {
	batch, err := service.Batch(id)
	if err != nil {
		return batch, err
	}
	if len(episodes) == 0 {
		return batch, requestError(errBatchEpisodeInvalid)
	}
	candidate := batch
	candidate.Episodes = episodes
	if err := validateBatch(candidate); err != nil {
		return batch, err
	}
	if len(batch.Episodes) > 0 {
		wanted, _ := json.Marshal(episodes)
		bound, _ := json.Marshal(batch.Episodes)
		if !bytes.Equal(wanted, bound) {
			return batch, requestError("批次目录已经绑定，不能替换")
		}
		return batch, nil
	}
	batch.Episodes = episodes
	path, _ := service.batchPath(id)
	return batch, saveTaskJSON(path, batch)
}

func (service *Service) CompleteBatch(id string) error {
	batch, err := service.Batch(id)
	if err != nil {
		return err
	}
	if len(batch.Episodes) == 0 {
		return requestError(errBatchEpisodeInvalid)
	}
	batch.Completed = true
	path, _ := service.batchPath(id)
	return saveTaskJSON(path, batch)
}

func (service *Service) PendingBatches() ([]Pending, error) {
	entries, err := os.ReadDir(filepath.Join(service.Root, ".batches"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result := []Pending{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-5]
		if !operationIDPattern.MatchString(id) {
			continue
		}
		batch, err := service.Batch(id)
		pending := Pending{ID: id, Name: "批量课件", Kind: "batch"}
		if batch.Input.Kind == "subtitle" {
			pending.Kind = "batch-subtitle"
			pending.Name = "批量字幕"
		}
		if err != nil {
			pending.Error = err.Error()
			result = append(result, pending)
			continue
		}
		if !batch.Completed {
			label := "批量课件"
			if batch.Input.Kind == "subtitle" {
				label = "批量字幕"
			}
			pending.Name = fmt.Sprintf("%s · %d 个分集", label, len(batch.Input.Pages))
			result = append(result, pending)
		}
	}
	return result, nil
}

// BatchChildID is stable within a parent batch; changing the parent ID is an
// explicit new operation, not a claim of cross-batch deduplication.
func BatchChildID(parent string, page int) string {
	return batchChildID("video-ppt-batch", parent, page)
}

func (batch BatchOperation) ChildID(page int) string {
	if batch.Input.Kind == "subtitle" {
		return batchChildID("video-subtitle-batch", batch.ID, page)
	}
	return BatchChildID(batch.ID, page)
}

func batchChildID(namespace, parent string, page int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", namespace, parent, page)))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	raw := hex.EncodeToString(sum[:16])
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}

const errBatchEpisodeInvalid = "批次分集记录无效"
