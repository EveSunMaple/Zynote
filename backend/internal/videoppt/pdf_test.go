package videoppt

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestExportPDFAndCrop(t *testing.T) {
	root := t.TempDir()
	taskID := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	directory := filepath.Join(root, taskID)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source, _ := json.Marshal(Source{ID: taskID, Name: "第一讲.mp4", Duration: 10, Width: 10, Height: 10,
		Preview: "p", Crop: Region{Left: 0, Right: 1, Top: 0, Bottom: 1}})
	if err := os.WriteFile(filepath.Join(directory, "source.json"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "frames.json"), []byte(`[{"page":1,"time":1,"file":"frame-1.png"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	frame := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			frame.Set(x, y, color.RGBA{R: 255, G: uint8(x * 20), B: uint8(y * 20), A: 255})
		}
	}
	file, err := os.Create(filepath.Join(directory, "frame-1.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, frame); err != nil {
		t.Fatal(err)
	}
	file.Close()
	output := t.TempDir()
	processor := Processor{Root: root, Platform: "darwin", AllowSystemFallback: true}
	result, data, err := processor.Export(context.Background(), CreatedTask{ID: taskID, Name: "第一讲.mp4", Dir: directory, Path: "video"}, []Choice{
		{Page: 1, Keep: true, Crop: Region{Left: 0.5, Right: 1, Top: 0, Bottom: 1}},
	}, output)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kept != 1 || result.Removed != 0 || !bytes.HasPrefix(data, []byte("%PDF-1.7")) ||
		bytes.Count(data, []byte("/Type /Page ")) != 1 || !bytes.Contains(data, []byte("/MediaBox [0 0 5 10]")) {
		t.Fatalf("export mismatch: %+v %q", result, data[:40])
	}
	if body, err := os.ReadFile(result.Path); err != nil || !bytes.Equal(body, data) {
		t.Fatalf("exported file mismatch: %v", err)
	}
	if filepath.Base(result.Path) != "第一讲-视频课件.pdf" {
		t.Fatalf("filename mismatch: %s", result.Path)
	}
}

func TestExportRejectsIncompleteChoices(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(directory, "frames.json"), []byte(`[]`), 0o644)
	processor := Processor{Root: root, Platform: "darwin"}
	if _, _, err := processor.Export(context.Background(), CreatedTask{Dir: directory}, []Choice{{Page: 1, Keep: true}}, t.TempDir()); err == nil {
		t.Fatal("incomplete choices must fail")
	}
}
