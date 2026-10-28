package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRelocateLibraryPathsMovesExistingTargets(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "assistant.db")
	write, err := OpenWrite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := BootstrapInto(write); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(root, "资料")
	library := filepath.Join(root, "Library")
	for _, path := range []string{
		filepath.Join(previous, "PPT", "课件.pdf"),
		filepath.Join(previous, "Transcripts", "原文.srt"),
		filepath.Join(previous, "MediaDownloads", "视频.mp4"),
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(library, path[len(previous)+1:])), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(library, path[len(previous)+1:]), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := write.db.Exec("INSERT INTO courses(id,name) VALUES('c','课程')"); err != nil {
		t.Fatal(err)
	}
	if _, err := write.db.Exec(
		"INSERT INTO course_files(id,course_id,kind,path) VALUES('f','c','past_exam',?)",
		filepath.Join(previous, "PPT", "课件.pdf"),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := write.db.Exec(
		"INSERT INTO lectures(id,course_id,ppt_path,transcript_path,source_video_path) VALUES('l','c',?,?,?)",
		filepath.Join(previous, "PPT", "课件.pdf"),
		filepath.Join(previous, "Transcripts", "原文.srt"),
		filepath.Join(previous, "MediaDownloads", "视频.mp4"),
	); err != nil {
		t.Fatal(err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RelocateLibraryPaths(dbPath, previous, library); err != nil {
		t.Fatal(err)
	}
	read, err := OpenWrite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	var path, ppt, transcript, source string
	if err := read.db.QueryRow("SELECT path FROM course_files WHERE id='f'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := read.db.QueryRow("SELECT ppt_path,transcript_path,source_video_path FROM lectures WHERE id='l'").Scan(&ppt, &transcript, &source); err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(library, "PPT", "课件.pdf") {
		t.Fatalf("course file not relocated: %s", path)
	}
	if ppt != filepath.Join(library, "PPT", "课件.pdf") || transcript != filepath.Join(library, "Transcripts", "原文.srt") || source != filepath.Join(library, "MediaDownloads", "视频.mp4") {
		t.Fatalf("lecture paths not relocated: %s %s %s", ppt, transcript, source)
	}
}
