package videoppt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExportPreparedAndPublishedResumeWithoutDuplicate(t *testing.T) {
	task := CreatedTask{Dir: t.TempDir()}
	output := t.TempDir()
	data := []byte("verified PDF fixture")
	result := ExportResult{FileName: "lecture", Kept: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := publishExport(ctx, task, nil, output, result, data); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	files, _ := os.ReadDir(output)
	if len(files) != 0 {
		t.Fatal("cancel published")
	}
	published, err := publishExport(context.Background(), task, nil, output, result, data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := publishExport(context.Background(), task, nil, output, result, data)
	if err != nil || again.Path != published.Path {
		t.Fatal("published recovery mismatch", again, err)
	}
	files, _ = os.ReadDir(output)
	if len(files) != 1 {
		t.Fatal("duplicate output")
	}
	if err := os.WriteFile(published.Path, []byte("user edits"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := publishExport(context.Background(), task, nil, output, result, data); err == nil {
		t.Fatal("changed output accepted")
	}
	files, _ = os.ReadDir(output)
	if len(files) != 1 {
		t.Fatal("new output after mutation")
	}
	body, _ := os.ReadFile(filepath.Join(output, "lecture-视频课件.pdf"))
	if string(body) != "user edits" {
		t.Fatal("user output overwritten")
	}
}
