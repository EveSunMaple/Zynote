//go:build windows && amd64

package im

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestNapcatZipValidationRejectsUnsafeEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entries := map[string]string{
		"../evil":       "x",
		"/absolute":     "x",
		"dir\\evil":     "x",
		"safe/file.txt": "ok",
	}
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		err := validateZipEntry(entry)
		if entry.Name == "safe/file.txt" && err != nil {
			t.Fatalf("safe entry rejected: %v", err)
		}
		if entry.Name != "safe/file.txt" && err == nil {
			t.Fatalf("unsafe entry accepted: %s", entry.Name)
		}
	}
}

func TestNapcatExtractComponentStaysInStage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("napcat/config/napcat.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(`{"fileLog":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	stage := t.TempDir()
	if err := extractComponent(archive, stage); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(stage, "napcat", "config", "napcat.json"))
	if err != nil || string(raw) != `{"fileLog":true}` {
		t.Fatalf("extracted content mismatch: %q %v", raw, err)
	}
}

func TestNapcatEnvironmentSanitizesInheritedOverrides(t *testing.T) {
	t.Setenv("NAPCAT_QUICK_ACCOUNT", "10001")
	t.Setenv("NODE_OPTIONS", "--inspect")
	t.Setenv("KEEP_ME", "1")
	values := sanitizedEnvironment()
	for _, item := range values {
		if item == "NAPCAT_QUICK_ACCOUNT=10001" || item == "NODE_OPTIONS=--inspect" {
			t.Fatalf("unsafe environment leaked: %s", item)
		}
		if item == "KEEP_ME=1" {
			return
		}
	}
	t.Fatal("unrelated environment was removed")
}
