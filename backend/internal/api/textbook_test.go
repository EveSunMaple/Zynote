package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTextbookDirectoryWalksUp(t *testing.T) {
	root := t.TempDir()
	textbook := filepath.Join(root, "课本")
	if err := os.MkdirAll(textbook, 0o755); err != nil {
		t.Fatal(err)
	}
	start := filepath.Join(root, "Contents", "MacOS")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := textbookDirectory(start); got != textbook {
		t.Fatalf("unexpected directory: %q", got)
	}
}

func TestTextbookDirectoryMissing(t *testing.T) {
	if got := textbookDirectory(t.TempDir()); got != "" {
		t.Fatalf("unexpected directory: %q", got)
	}
}
