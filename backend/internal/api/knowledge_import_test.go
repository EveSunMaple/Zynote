package api

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestReadKnowledgeFileParsesDocxAndLegacyEncoding(t *testing.T) {
	directory := t.TempDir()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	part, err := writer.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(`<w:document><w:body><w:p><w:r><w:t>牙髓病学讲义</w:t></w:r></w:p></w:body></w:document>`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	docx := filepath.Join(directory, "讲义.docx")
	if err := os.WriteFile(docx, archive.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	input, err := readKnowledgeFile(nil, docx)
	if err != nil || !strings.Contains(input.Text, "牙髓病学讲义") || input.Title != "讲义.docx" || input.Locator != docx {
		t.Fatalf("docx import mismatch: %+v %v", input, err)
	}

	encoded, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("旧版记事本保存的根管治疗笔记，内容较长以便识别编码。"))
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(directory, "笔记.txt")
	if err := os.WriteFile(legacy, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	input, err = readKnowledgeFile(nil, legacy)
	if err != nil || !strings.Contains(input.Text, "根管治疗笔记") || len(input.SourceHash) != 64 || input.Origin != "local" {
		t.Fatalf("GB18030 import mismatch: %+v %v", input, err)
	}
}

func TestReadKnowledgeFileRejectsOversizedText(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "large.txt")
	if err := os.WriteFile(path, make([]byte, knowledgeTextMaxBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readKnowledgeFile(nil, path); !errors.Is(err, errKnowledgeTextTooLarge) {
		t.Fatalf("oversized text should be rejected: %v", err)
	}
}

func TestReadKnowledgeFileRejectsUnsupportedInputs(t *testing.T) {
	directory := t.TempDir()
	if _, err := readKnowledgeFile(nil, directory); !errors.Is(err, errKnowledgeFileUnreadable) {
		t.Fatalf("directory should be rejected: %v", err)
	}
	slides := filepath.Join(directory, "课件.pptx")
	if err := os.WriteFile(slides, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readKnowledgeFile(nil, slides); !errors.Is(err, errKnowledgeFileType) {
		t.Fatalf("pptx should be rejected: %v", err)
	}
	scan := filepath.Join(directory, "扫描.pdf")
	if err := os.WriteFile(scan, []byte("%PDF-1.4\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readKnowledgeFile(nil, scan); err == nil || !strings.Contains(err.Error(), "PDF") {
		t.Fatalf("pdf without parser should fail: %v", err)
	}
}
