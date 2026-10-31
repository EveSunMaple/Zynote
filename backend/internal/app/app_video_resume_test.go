package app_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoResumeSavedSubtitle(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	resourcesRoot := t.TempDir()
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": testPlatform(), "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	if _, errReply := f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("bootstrap failed: %+v", errReply)
	}
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "字幕课"})
	if errReply != nil {
		t.Fatal(errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	_ = course["id"]
	directory := filepath.Join(libraryRoot, "Transcripts", "字幕课")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	srt := "1\n00:00:01,000 --> 00:00:02,000\n已保存字幕\n"
	path := filepath.Join(directory, "P1-离线转写.srt")
	if err := os.WriteFile(path, []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(srt))
	id := "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	taskDir := filepath.Join(libraryRoot, "VideoTasks", id)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "video.mp4"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}
	record, _ := json.Marshal(map[string]any{
		"id": id, "name": "第一讲.mp4", "kind": "subtitle", "outputName": "第一讲-离线转写.srt",
		"courseName": "字幕课", "page": 1,
		"saved": map[string]any{"path": path, "sha256": hex.EncodeToString(sum[:])},
	})
	if err := os.WriteFile(filepath.Join(taskDir, "task.json"), record, 0o644); err != nil {
		t.Fatal(err)
	}
	raw, errReply := f.call("video-ppt:resume-subtitle", map[string]any{"id": id})
	if errReply != nil {
		t.Fatalf("resume failed: %+v", errReply)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result["srt"] != srt || result["count"] != float64(1) || result["savedPath"] != path ||
		result["source"] != "已保存的字幕" {
		t.Fatalf("resume mismatch: %s", raw)
	}
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Fatalf("task dir should be discarded: %v", err)
	}
	raw, errReply = f.call("video-ppt:pending", nil)
	if errReply != nil || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("pending should be empty: %s %+v", raw, errReply)
	}
}
