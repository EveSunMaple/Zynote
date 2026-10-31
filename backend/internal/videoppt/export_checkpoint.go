package videoppt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type exportCheckpoint struct {
	Version int          `json:"version"`
	Input   string       `json:"input"`
	SHA256  string       `json:"sha256"`
	Result  ExportResult `json:"result"`
}

func publishExport(ctx context.Context, task CreatedTask, choices []Choice, directory string, result ExportResult, data []byte) (ExportResult, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return ExportResult{}, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	input, _ := json.Marshal(struct {
		Choices           []Choice
		Directory, Digest string
	}{choices, directory, digest})
	keySum := sha256.Sum256(input)
	key := hex.EncodeToString(keySum[:])
	journal := filepath.Join(task.Dir, "export-"+key+".json")
	var record exportCheckpoint
	body, err := os.ReadFile(journal)
	if err == nil {
		if json.Unmarshal(body, &record) != nil || record.Version != 1 || record.Input != key || record.SHA256 != digest || filepath.Dir(record.Result.Path) != directory || filepath.Ext(record.Result.Path) != ".pdf" {
			return ExportResult{}, requestError("PDF 导出记录无效，原文件已保留")
		}
		result = record.Result
	} else if !os.IsNotExist(err) {
		return ExportResult{}, err
	} else {
		path, err := availableExport(directory, result.FileName+"-视频课件.pdf")
		if err != nil {
			return ExportResult{}, err
		}
		result.Path = path
		record = exportCheckpoint{Version: 1, Input: key, SHA256: digest, Result: result}
		if err := saveTaskJSON(journal, record); err != nil {
			return ExportResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return ExportResult{}, err
	}
	existing, err := os.ReadFile(result.Path)
	if err == nil {
		if !bytes.Equal(existing, data) {
			return ExportResult{}, requestError("PDF 与导出记录不一致，请保留并核对，不能重复导出")
		}
		return result, nil
	}
	if !os.IsNotExist(err) {
		return ExportResult{}, err
	}
	if err := stageVideo(ctx, bytes.NewReader(data), result.Path); err != nil {
		return ExportResult{}, err
	}
	existing, err = os.ReadFile(result.Path)
	if err != nil || !bytes.Equal(existing, data) {
		return ExportResult{}, requestError(errPdfSaveCheck)
	}
	return result, nil
}

func availableExport(directory, name string) (string, error) {
	extension := filepath.Ext(name)
	stem := name[:len(name)-len(extension)]
	for index := 1; ; index++ {
		candidate := filepath.Join(directory, stem+extension)
		if index > 1 {
			candidate = filepath.Join(directory, fmt.Sprintf("%s (%d)%s", stem, index, extension))
		}
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", requestError("导出路径不可检查")
		}
	}
}
