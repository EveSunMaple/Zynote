package app_test

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	// Register the SQLite driver used by database/sql.
	_ "modernc.org/sqlite"
)

func writeLectureFixture(t *testing.T, libraryRoot string) (transcriptPath, pptPath string) {
	t.Helper()
	transcriptPath = filepath.Join(libraryRoot, "Transcripts", "lecture-source.txt")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		transcriptPath,
		[]byte(strings.Join([]string{"课程", "1. 语音识别", "00:00:01 开场内容", "00:00:08 重点内容", "补充说明"}, "\n")),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	pptPath = filepath.Join(libraryRoot, "ppt-upload.pptx")
	pptx := map[string]string{
		"ppt/presentation.xml":             `<p:presentation><p:sldIdLst><p:sldId id="1" r:id="r1"/></p:sldIdLst></p:presentation>`,
		"ppt/_rels/presentation.xml.rels":  `<Relationships><Relationship Id="r1" Target="slides/slide1.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":            `<a:t>课件标题内容</a:t>`,
		"ppt/slides/_rels/slide1.xml.rels": `<Relationships/>`,
	}
	var buffer strings.Builder
	_ = buffer
	writePptx(t, pptPath, pptx)
	return transcriptPath, pptPath
}

func TestP3eLectureChain(t *testing.T) {
	dataRoot := t.TempDir()
	libraryRoot := t.TempDir()
	resourcesRoot := t.TempDir()

	f := startCore(t, dataRoot, resourcesRoot)
	_, errReply := f.call("core.hello", map[string]any{
		"protocol": 1, "appVersion": "0.0.1", "electronVersion": "42.11.10",
		"platform": "darwin", "dataRoot": dataRoot, "libraryRoot": libraryRoot,
		"resourcesRoot": resourcesRoot, "promptsRoot": resourcesRoot, "portable": false,
	})
	if errReply != nil {
		t.Fatalf("hello failed: %+v", errReply)
	}
	if _, errReply = f.call("db:bootstrap", nil); errReply != nil {
		t.Fatalf("db:bootstrap failed: %+v", errReply)
	}

	course, errReply := f.call("course:create", map[string]any{"name": "链路课"})
	if errReply != nil {
		t.Fatalf("course:create failed: %+v", errReply)
	}
	var created map[string]any
	if err := json.Unmarshal(course, &created); err != nil {
		t.Fatal(err)
	}
	courseID := created["id"].(string)
	transcriptPath, pptPath := writeLectureFixture(t, libraryRoot)

	lectureReply, errReply := f.call("lecture:create", map[string]any{
		"courseId":       courseID,
		"title":          "第一讲",
		"transcriptPath": transcriptPath,
		"pptPath":        pptPath,
	})
	if errReply != nil {
		t.Fatalf("lecture:create failed: %+v", errReply)
	}
	var lecture map[string]any
	if err := json.Unmarshal(lectureReply, &lecture); err != nil {
		t.Fatal(err)
	}
	lectureID := lecture["id"].(string)
	if lecture["status"] != "待校订" || lecture["time_estimated"] != float64(1) {
		t.Fatalf("lecture fields mismatch: %v", lecture)
	}
	if !strings.Contains(lecture["transcript_path"].(string), filepath.Join("Transcripts", "链路课")) {
		t.Fatalf("transcript copy location wrong: %v", lecture["transcript_path"])
	}
	if !strings.Contains(lecture["ppt_path"].(string), filepath.Join("PPT", "链路课")) {
		t.Fatalf("ppt copy location wrong: %v", lecture["ppt_path"])
	}
	if !strings.Contains(lecture["ppt_text"].(string), "课件标题内容") ||
		!strings.Contains(lecture["ppt_text"].(string), "bilisum-courseware-cache:v1:") {
		t.Fatalf("ppt cache serialization broken: %q", lecture["ppt_text"])
	}
	if lecture["source_video_path"].(string) != "" || lecture["video_path"].(string) != "" {
		t.Fatalf("no video expected: %v", lecture)
	}

	placeholderDetail, errReply := f.call("course:detail", map[string]any{"id": courseID})
	if errReply != nil {
		t.Fatalf("detail failed: %+v", errReply)
	}
	var detail map[string]any
	if err := json.Unmarshal(placeholderDetail, &detail); err != nil {
		t.Fatal(err)
	}
	sessions := detail["sessions"].([]any)
	defaultRenamed := false
	for _, item := range sessions {
		if item.(map[string]any)["title"] == "第一讲" && item.(map[string]any)["lecture_id"] == lectureID {
			defaultRenamed = true
		}
	}
	if !defaultRenamed {
		t.Fatalf("placeholder session should be renamed to lecture title: %v", sessions)
	}

	reImport, errReply := f.call("lecture:transcript", map[string]any{
		"id":   lectureID,
		"path": transcriptPath,
	})
	if errReply != nil {
		t.Fatalf("lecture:transcript failed: %+v", errReply)
	}
	var refreshed map[string]any
	if err := json.Unmarshal(reImport, &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed["lecture"].(map[string]any)["transcript_text"] == "" {
		t.Fatalf("transcript text missing after reimport")
	}

	coursewareReply, errReply := f.call("lecture:courseware", map[string]any{
		"id":   lectureID,
		"path": pptPath,
	})
	if errReply != nil {
		t.Fatalf("lecture:courseware failed: %+v", errReply)
	}
	var coursewareDetail map[string]any
	if err := json.Unmarshal(coursewareReply, &coursewareDetail); err != nil {
		t.Fatal(err)
	}
	updatedLecture := coursewareDetail["lecture"].(map[string]any)
	if updatedLecture["ppt_processed"] != float64(0) {
		t.Fatalf("ppt_processed should reset: %v", updatedLecture)
	}

	questionID := "66666666-6666-4666-8666-666666666666"
	db, err := sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(
		"INSERT INTO past_questions(id,course_id,file_id,question_json) VALUES(?,?,'',?)",
		questionID, courseID,
		`{"id":"`+questionID+`","courseId":"`+courseID+`","sourceFile":"卷.pdf","type":"填空","stem":"原题","knowledgePoints":["极限"]}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	saved, errReply := f.call("questions:save", map[string]any{
		"courseId": courseID,
		"questions": []map[string]any{{
			"id":              questionID,
			"courseId":        courseID,
			"sourceFile":      "卷.pdf",
			"type":            "填空",
			"stem":            "改后的题",
			"knowledgePoints": []string{"极限", "导数"},
		}},
	})
	if errReply != nil {
		t.Fatalf("questions:save failed: %+v", errReply)
	}
	if saved == nil {
		t.Fatalf("questions:save should return true")
	}
	db, err = sql.Open("sqlite", filepath.Join(dataRoot, "assistant.db"))
	if err != nil {
		t.Fatal(err)
	}
	var storedJSON string
	if err := db.QueryRow("SELECT question_json FROM past_questions WHERE id=?", questionID).Scan(&storedJSON); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(storedJSON, "改后的题") || !strings.Contains(storedJSON, "导数") {
		t.Fatalf("question update missing: %q", storedJSON)
	}

	foreign := map[string]any{"id": questionID, "courseId": "99999999-9999-4999-8999-999999999999", "stem": "x"}
	if _, errReply = f.call("questions:save", map[string]any{"courseId": courseID, "questions": []map[string]any{foreign}}); errReply == nil {
		t.Fatalf("foreign question should fail")
	}

	_, errReply = f.call("questions:save", map[string]any{"courseId": courseID, "questions": []map[string]any{}})
	if errReply != nil {
		t.Fatalf("empty questions should clear: %+v", errReply)
	}
	var remaining int
	if err := db.QueryRow("SELECT COUNT(*) FROM past_questions WHERE course_id=?", courseID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("questions should be removed, got %d", remaining)
	}
	_ = db.Close()
}

func writePptx(t *testing.T, path string, files map[string]string) {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, content := range files {
		file, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
