package app_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestHelloBacksUpAndRelocatesLegacyLibrary(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "资料")
	library := filepath.Join(root, "Library")
	if err := os.MkdirAll(filepath.Join(legacy, "PPT"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "PPT", "old.pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE courses(id TEXT PRIMARY KEY,name TEXT NOT NULL,created_at TEXT DEFAULT CURRENT_TIMESTAMP)",
		"CREATE TABLE course_files(id TEXT PRIMARY KEY,course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,kind TEXT NOT NULL,filename TEXT,path TEXT,text TEXT,chunks_json TEXT,needs_ocr INTEGER DEFAULT 0)",
		"CREATE TABLE lectures(id TEXT PRIMARY KEY,course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,title TEXT,video_path TEXT,ppt_path TEXT,ppt_text TEXT,status TEXT DEFAULT '待转写',created_at TEXT DEFAULT CURRENT_TIMESTAMP)",
		"INSERT INTO courses(id,name) VALUES('c','课程')",
		"INSERT INTO course_files(id,course_id,kind,path) VALUES('f','c','past_exam','" + filepath.Join(legacy, "PPT", "old.pdf") + "')",
		"INSERT INTO lectures(id,course_id,title,ppt_path) VALUES('l','c','课次','" + filepath.Join(legacy, "PPT", "old.pdf") + "')",
		"PRAGMA user_version = 0",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(legacy, library); err != nil {
		t.Fatal(err)
	}
	f := startCore(t, root, t.TempDir())
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": testPlatform(), "dataRoot": root, "libraryRoot": library,
		"previousLibraryRoot": legacy, "resourcesRoot": t.TempDir(), "promptsRoot": t.TempDir(),
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	db, err = sql.Open("sqlite", filepath.Join(root, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("unexpected schema version: %d", version)
	}
	var path, ppt string
	if err := db.QueryRow("SELECT path FROM course_files WHERE id='f'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT ppt_path FROM lectures WHERE id='l'").Scan(&ppt); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(library, "PPT", "old.pdf")
	if path != want || ppt != want {
		t.Fatalf("legacy paths not relocated: %q %q", path, ppt)
	}
	backups, err := os.ReadDir(filepath.Join(root, "Backups"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected one backup, got %v (%v)", backups, err)
	}
	backup, err := sql.Open("sqlite", filepath.Join(root, "Backups", backups[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if err := backup.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 0 {
		t.Fatalf("backup should preserve old version, got %d", version)
	}
	if err := backup.QueryRow("SELECT path FROM course_files WHERE id='f'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(legacy, "PPT", "old.pdf") {
		t.Fatalf("backup should preserve old path, got %q", path)
	}
}
