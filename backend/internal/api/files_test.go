package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyIntoReusesFileAlreadyInDestination(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "lecture.pptx")
	if err := os.WriteFile(source, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := copyInto(directory, source)
	if err != nil {
		t.Fatal(err)
	}
	if path != source {
		t.Fatalf("expected existing path %s, got %s", source, path)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("duplicate import created %d files", len(entries))
	}
}
