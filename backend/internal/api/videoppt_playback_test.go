package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivatePlaybackInputRejectsUnknownLocatorAndMalformedFields(t *testing.T) {
	inv := &Invoker{}
	state := &contract.State{LibraryRoot: t.TempDir()}
	for _, raw := range []string{`{}`, `null`, `{"id":"../escape","expectedSourceSHA256":"x"}`, `{"id":1}`, `{"id":"9e326025-30b4-45d0-bd27-4a30275adccb","expectedSourceSHA256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","path":"secret"}`, `{} {}`} {
		_, err := inv.handleVideoPlaybackSource(nil, state, json.RawMessage(raw))
		var application *contract.AppError
		if !errors.As(err, &application) || application.Code != contract.CodeInputInvalid {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	if _, ok := registerActions(inv)["video-ppt:playback-source"]; !ok {
		t.Fatal("private action missing")
	}
}

func TestPrivatePlaybackUsesActualLibraryVideoTasksAndSmallProjection(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := "9e326025-30b4-45d0-bd27-4a30275adccb"
	directory := filepath.Join(root, "VideoTasks", id)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("media"))
	hash := hex.EncodeToString(digest[:])
	operation := videoppt.VideoOperation{Version: 1, Stage: "source", Path: filepath.Join(root, "original.mp4"), SourceHash: hash}
	for name, value := range map[string]any{"operation.json": operation, "video-ready.json": map[string]any{"version": 1, "sha256": hash}} {
		body, _ := json.Marshal(value)
		if err := os.WriteFile(filepath.Join(directory, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "video.mp4"), []byte("media"), 0600); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]string{"id": id, "expectedSourceSHA256": hash})
	result, err := (&Invoker{}).handleVideoPlaybackSource(nil, &contract.State{LibraryRoot: root}, params)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(result)
	var fields map[string]any
	_ = json.Unmarshal(body, &fields)
	if len(fields) != 8 || fields["verification"] != "declared" || fields["sourceSHA256"] != hash || fields["taskID"] != id || fields["path"] != nil || fields["relativePath"] != nil {
		t.Fatalf("projection: %s", body)
	}
}
