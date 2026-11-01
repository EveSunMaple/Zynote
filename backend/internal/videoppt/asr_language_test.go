package videoppt

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSubtitleLanguageBindingSurvivesSaveAndRejectsChangedIdentity(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(root)
	operation := VideoOperation{Version: 1, Stage: "subtitle", URL: "https://www.bilibili.com/video/BV1xx411c7mD/", Page: 1, OutputName: "words.srt", ASRLanguage: "en"}
	task, err := service.CreateOperationTask("", "lecture", operation)
	if err != nil {
		t.Fatal(err)
	}
	meta, _, err := service.SubtitleTask(task.ID)
	if err != nil || meta.ASRLanguage != "en" {
		t.Fatal(meta, err)
	}
	path := filepath.Join(t.TempDir(), "words.srt")
	if err := os.WriteFile(path, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := service.MarkSubtitleSaved(task.ID, path); err != nil {
		t.Fatal(err)
	}
	meta, _, err = service.SubtitleTask(task.ID)
	if err != nil || meta.ASRLanguage != "en" || meta.Saved == nil {
		t.Fatal(meta, err)
	}
	changed := operation
	changed.ASRLanguage = "auto"
	if _, err := service.CreateOperationTask(task.ID, "lecture", changed); err == nil {
		t.Fatal("rebound language")
	}
	if err := service.CompleteOperation(task, map[string]string{"savedPath": path}, path); err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateOperationTask(task.ID, "lecture", operation)
	var completed *CompletedOperationError
	if !errors.As(err, &completed) {
		t.Fatal("same-language completion not reused", err)
	}
	if _, err := service.CreateOperationTask(task.ID, "lecture", changed); err == nil || errors.As(err, &completed) {
		t.Fatal("changed-language completion reused", err)
	}
	// Independently tampered task metadata must not override the operation.
	meta.ASRLanguage = "auto"
	if err := saveTaskJSON(filepath.Join(task.Dir, taskFileName), meta); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.SubtitleTask(task.ID); err == nil {
		t.Fatal("task/operation mismatch accepted")
	}
}

func TestLegacyChineseOperationAndBatchRemainByteCompatible(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(root)
	operation := VideoOperation{Version: 1, Stage: "subtitle", URL: "https://www.bilibili.com/video/BV1xx411c7mD/", Page: 1, OutputName: "words.srt"}
	task, err := service.CreateOperationTask("", "lecture", operation)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(task.Dir, "operation.json"))
	operation.ASRLanguage = "zh"
	if _, err := service.CreateOperationTask(task.ID, "lecture", operation); err != nil {
		t.Fatal("explicit zh changed legacy binding", err)
	}
	after, _ := os.ReadFile(filepath.Join(task.Dir, "operation.json"))
	if string(before) != string(after) {
		t.Fatal("legacy operation rewritten")
	}
	input := BatchInput{Kind: "subtitle", URL: operation.URL, Pages: []int{1}, Prompt: "station-first-then-asr"}
	batch, err := service.BindBatch("", input)
	if err != nil {
		t.Fatal(err)
	}
	input.ASRLanguage = "zh"
	if _, err := service.BindBatch(batch.ID, input); err != nil {
		t.Fatal(err)
	}
	input.ASRLanguage = "en"
	if _, err := service.BindBatch(batch.ID, input); err == nil {
		t.Fatal("batch language rebound")
	}
	var raw map[string]any
	if err := json.Unmarshal(before, &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["asrLanguage"]; exists {
		t.Fatal("legacy default unexpectedly persisted")
	}
}
