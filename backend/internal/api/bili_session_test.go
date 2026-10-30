package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyLearningVideo(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	dest := filepath.Join(root, "video.mp4")
	if err := os.WriteFile(source, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyLearningVideo(context.Background(), source, dest); err != nil {
		t.Fatal(err)
	}
	if err := copyLearningVideo(context.Background(), source, dest); err == nil {
		t.Fatal("existing task overwritten")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := copyLearningVideo(ctx, source, filepath.Join(root, "cancelled.mp4")); err == nil {
		t.Fatal("cancel ignored")
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "fixture" {
		t.Fatal("copy mismatch")
	}
}
