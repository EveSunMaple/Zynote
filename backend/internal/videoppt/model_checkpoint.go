package videoppt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

type modelCheckpoint struct {
	Version  int    `json:"version"`
	Input    string `json:"input"`
	State    string `json:"state"`
	Response string `json:"response,omitempty"`
}

func saveModelCheckpoint(path string, record modelCheckpoint) error {
	return saveTaskJSON(path, record)
}

func saveTaskJSON(path string, record any) error {
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".model-checkpoint-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// CheckpointChat binds each model stage to its exact request. Completed stages
// reuse their response; a lost response cannot silently dispatch a second call.
// The task remains owned by the caller's exclusive worker.
func CheckpointChat(task CreatedTask, stage, identity string, chat ChatFunc) ChatFunc {
	return func(ctx context.Context, system, text string, images []Image) (string, error) {
		if stage != "source" && stage != "extract" {
			return "", requestError("模型阶段无效")
		}
		encoded, err := json.Marshal(struct {
			Identity string
			System   string
			Text     string
			Images   []Image
		}{identity, system, text, images})
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(encoded)
		key := hex.EncodeToString(sum[:])
		path := filepath.Join(task.Dir, "model-"+stage+"-"+key+".json")
		// A prior unknown call blocks revisions too; changing the crop, prompt or
		// connection cannot bypass an unresolved provider outcome.
		paths, err := filepath.Glob(filepath.Join(task.Dir, "model-"+stage+"-*.json"))
		if err != nil {
			return "", err
		}
		var cached *modelCheckpoint
		for _, existing := range paths {
			body, err := os.ReadFile(existing)
			if err != nil {
				return "", err
			}
			var stored modelCheckpoint
			if json.Unmarshal(body, &stored) != nil || stored.Version != 1 || stored.Input == "" || filepath.Base(existing) != "model-"+stage+"-"+stored.Input+".json" {
				return "", requestError("模型阶段记录无效，任务已保留")
			}
			if stored.State == "dispatched" {
				return "", requestError("模型请求结果未知，不能重复调用；请保留任务并核对服务端结果")
			}
			if stored.State != "prepared" {
				return "", requestError("模型阶段状态无效")
			}
			if stored.Input == key {
				copy := stored
				cached = &copy
			}
		}
		if cached != nil {
			return cached.Response, nil
		}
		var record modelCheckpoint
		if err := ctx.Err(); err != nil {
			return "", err
		}
		record = modelCheckpoint{Version: 1, Input: key, State: "dispatched"}
		if err := saveModelCheckpoint(path, record); err != nil {
			return "", err
		}
		response, err := chat(ctx, system, text, images)
		if err != nil {
			return "", err
		}
		// Save a known response even if cancellation arrived with it. Recovery can
		// reuse it without paying again; callers still observe cancellation.
		record.State = "prepared"
		record.Response = response
		if err := saveModelCheckpoint(path, record); err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return response, nil
	}
}
