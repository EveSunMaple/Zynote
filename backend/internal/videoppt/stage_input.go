package videoppt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// PersistStageInput records immutable domain inputs before queue admission.
// Each explicit revision gets its own content-addressed record; identical
// submissions preserve the same record.
func PersistStageInput(task CreatedTask, stage string, input any) error {
	switch stage {
	case "source", "auto", "extract", "export", "subtitle":
	default:
		return requestError("任务阶段无效")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	path := filepath.Join(task.Dir, "input-"+stage+"-"+hex.EncodeToString(sum[:])+".json")
	if err := stageVideo(context.Background(), bytes.NewReader(body), path); err != nil {
		if !os.IsExist(err) {
			return err
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, body) {
			return requestError("任务阶段参数已改变，请新建任务；已有资料保留")
		}
	}
	return nil
}
