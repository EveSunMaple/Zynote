package app_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryDelete(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()
	f := startCore(t, dataRoot, resourcesRoot)
	if _, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": testPlatform(), "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false, "packaged": false,
	}); errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	if _, errReply := f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("bootstrap failed: %+v", errReply)
	}
	courseRaw, errReply := f.call("course:create", map[string]any{"name": "删除文件课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var course map[string]any
	_ = json.Unmarshal(courseRaw, &course)
	courseID := course["id"].(string)
	directory := filepath.Join(libraryRoot, "Notes", "删除文件课")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "旧讲义.pdf")
	if err := os.WriteFile(path, []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		"INSERT INTO course_files(id,course_id,kind,filename,path,text,chunks_json,needs_ocr) VALUES(?,?,?,?,?,?,?,?)",
		"66666666-6666-4666-8666-666666666666", courseID, "senior_notes", "旧讲义.pdf", path, "", "[]", 0,
	); err != nil {
		t.Fatal(err)
	}
	lectureID := "77777777-7777-4777-8777-777777777777"
	if _, err = db.Exec(
		"INSERT INTO lectures(id,course_id,title,ppt_path,ppt_text) VALUES(?,?,?,?,?)",
		lectureID, courseID, "旧课", path, "旧文本",
	); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, errReply := f.call("library:delete", map[string]any{"path": path}); errReply == nil {
		t.Fatal("unconfirmed delete must fail")
	}
	if _, errReply := f.call("library:delete", map[string]any{"path": path, "confirmed": true}); errReply != nil {
		t.Fatalf("delete failed: %+v", errReply)
	}
	db, err = sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var files, pptPath int
	if err := db.QueryRow("SELECT count(*) FROM course_files WHERE path=?", path).Scan(&files); err != nil || files != 0 {
		t.Fatalf("course_files not cleaned: %d %v", files, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM lectures WHERE id=? AND ppt_path<>'' AND ppt_text<>''", lectureID).
		Scan(&pptPath); err != nil || pptPath != 0 {
		t.Fatalf("lecture ppt not cleared: %d %v", pptPath, err)
	}
}
