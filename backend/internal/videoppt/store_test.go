package videoppt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTaskFile(t *testing.T, root, id, name, content string) {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPendingAndRestore(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(root)
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	source := `{"id":"` + id + `","name":"第一讲","duration":12.5,"width":1280,"height":720,"preview":"data:image/jpeg;base64,xx","crop":{"left":0.1,"right":0.9,"top":0.1,"bottom":0.9},"warning":""}`
	writeTaskFile(t, root, id, "task.json", `{"id":"`+id+`","name":"第一讲"}`)
	writeTaskFile(t, root, id, "source.json", source)
	writeTaskFile(t, root, id, "frames.json", `[{"page":1,"time":1.5,"file":"frame-1.png"}]`)
	writeTaskFile(t, root, id, "review.json", `{"id":"`+id+`","sourceName":"第一讲","pages":[{"page":1,"time":1.5,"width":100,"height":80,"preview":"data:image/png;base64,xx","crop":{"left":0.1,"right":0.9,"top":0.1,"bottom":0.9},"keep":true,"reason":"ok"}]}`)
	pending, err := service.Pending()
	if err != nil || len(pending) != 1 || pending[0].Name != "第一讲" || pending[0].Kind != "" {
		t.Fatalf("pending mismatch: %+v %v", pending, err)
	}
	restored, err := service.Restore(id)
	if err != nil {
		t.Fatal(err)
	}
	review, ok := restored["review"].(*Review)
	if !ok || len(review.Pages) != 1 || review.Pages[0].Page != 1 {
		t.Fatalf("restore mismatch: %+v", restored)
	}
	sourceValue, ok := restored["source"].(Source)
	if !ok || sourceValue.Name != "第一讲" || sourceValue.Width != 1280 {
		t.Fatalf("source mismatch: %+v", restored)
	}
}

func TestPendingSubtitleAndBroken(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(root)
	subtitleID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	writeTaskFile(t, root, subtitleID, "task.json", `{"id":"`+subtitleID+`","name":"P1 字幕","kind":"subtitle","outputName":"P1.srt","page":1,"saved":{"path":"/tmp/P1.srt","sha256":"`+strings.Repeat("a", 64)+`"}}`)
	brokenID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	writeTaskFile(t, root, brokenID, "task.json", `{"id":"`+brokenID+`","name":"损坏任务"}`)
	pending, err := service.Pending()
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending mismatch: %+v %v", pending, err)
	}
	values := map[string]Pending{}
	for _, item := range pending {
		values[item.ID] = item
	}
	if values[subtitleID].Kind != "subtitle" || values[subtitleID].SavedPath != "/tmp/P1.srt" {
		t.Fatalf("subtitle mismatch: %+v", values[subtitleID])
	}
	if values[brokenID].Error == "" || values[brokenID].Name != "损坏任务" {
		t.Fatalf("broken mismatch: %+v", values[brokenID])
	}
}

func TestRestoreRejectsMismatchedReview(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := New(root)
	id := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	source := `{"id":"` + id + `","name":"x","duration":1,"width":100,"height":100,"preview":"p","crop":{"left":0,"right":1,"top":0,"bottom":1},"warning":""}`
	writeTaskFile(t, root, id, "task.json", `{"id":"`+id+`","name":"x"}`)
	writeTaskFile(t, root, id, "source.json", source)
	writeTaskFile(t, root, id, "frames.json", `[{"page":1,"time":0,"file":"frame-1.png"}]`)
	writeTaskFile(t, root, id, "review.json", `{"id":"`+id+`","sourceName":"x","pages":[{"page":2,"time":0,"width":10,"height":10,"preview":"p","crop":{"left":0,"right":1,"top":0,"bottom":1},"keep":true,"reason":""}]}`)
	if _, err := service.Restore(id); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("mismatch guard failed: %v", err)
	}
	data, _ := json.Marshal(Pending{ID: id, Name: "x"})
	if !strings.Contains(string(data), `"id":"`+id+`"`) {
		t.Fatalf("pending serialization broken: %s", data)
	}
}
