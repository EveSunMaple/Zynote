package api

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func writeZip(t *testing.T, path string, names ...string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte("test")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateZhiyunDownloadRequiresOfficePayload(t *testing.T) {
	root := t.TempDir()
	validPPTX := filepath.Join(root, "valid.pptx")
	writeZip(t, validPPTX, "[Content_Types].xml", "ppt/presentation.xml")
	if err := validateZhiyunDownload(validPPTX, "ppt", "valid.pptx"); err != nil {
		t.Fatalf("valid pptx rejected: %v", err)
	}
	fakePPTX := filepath.Join(root, "fake.pptx")
	writeZip(t, fakePPTX, "readme.txt")
	if err := validateZhiyunDownload(fakePPTX, "ppt", "fake.pptx"); err == nil {
		t.Fatal("zip without ppt payload must be rejected")
	}
	video := filepath.Join(root, "video.pptx")
	if err := os.WriteFile(video, []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'm', 'p', '4', '2'}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateZhiyunDownload(video, "ppt", "video.pptx"); err == nil {
		t.Fatal("video payload must be rejected")
	}
}
