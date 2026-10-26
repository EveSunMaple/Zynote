package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
)

type WriteDB struct {
	db *sql.DB
}

func NewID() string { return ids.New() }

func (w *WriteDB) NewID() string { return ids.New() }

func OpenWrite(path string) (*WriteDB, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &WriteDB{db: db}, nil
}

func (w *WriteDB) Close() error { return w.db.Close() }

func (w *WriteDB) Checkpoint() error {
	_, err := w.db.Exec("PRAGMA wal_checkpoint(FULL)")
	return err
}

func BootstrapInto(w *WriteDB) error { return Bootstrap(w.db) }

func (w *WriteDB) Course(id string) (map[string]any, error) {
	course, err := queryOne(w.db, "SELECT * FROM courses WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	if course == nil {
		return nil, contract.NewError(contract.CodeNotFound, "课程不存在")
	}
	return course, nil
}

func (w *WriteDB) CreateCourse(name string) (map[string]any, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, contract.InputInvalid("课程名称不能为空")
	}
	id := NewID()
	tx, err := w.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO courses(id,name) VALUES(?,?)", id, trimmed); err != nil {
		return nil, err
	}
	if _, err := createSessionTx(tx, id, "", ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return w.Course(id)
}

func (w *WriteDB) Sessions() ([]map[string]any, error) {
	return queryAll(w.db, "SELECT * FROM chat_sessions ORDER BY rowid")
}

func (w *WriteDB) CourseName(id string) (string, error) {
	course, err := w.Course(id)
	if err != nil {
		return "", err
	}
	return ToStringValue(course["name"]), nil
}

func (w *WriteDB) InsertFile(file map[string]any) error {
	_, err := w.db.Exec(
		"INSERT INTO course_files(id,course_id,kind,filename,path,text,chunks_json,needs_ocr) VALUES(?,?,?,?,?,?,?,?)",
		ToStringValue(file["id"]),
		ToStringValue(file["course_id"]),
		ToStringValue(file["kind"]),
		ToStringValue(file["filename"]),
		ToStringValue(file["path"]),
		ToStringValue(file["text"]),
		ToStringValue(file["chunks_json"]),
		file["needs_ocr"],
	)
	return err
}

func (w *WriteDB) RemoveFile(courseID, id string) error {
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM course_files WHERE course_id=? AND id=?", courseID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(
		"DELETE FROM practice_sets WHERE lecture_id IN (SELECT id FROM lectures WHERE course_id=?)",
		courseID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *WriteDB) CreateLecture(courseID, title, video, ppt, pptText, sourceVideo string) (map[string]any, error) {
	if _, err := w.Course(courseID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		return nil, contract.InputInvalid("请填写课次标题")
	}
	id := NewID()
	tx, err := w.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"INSERT INTO lectures(id,course_id,title,video_path,ppt_path,ppt_text,source_video_path) VALUES(?,?,?,?,?,?,?)",
		id, courseID, title, video, ppt, pptText, sourceVideo,
	); err != nil {
		return nil, err
	}
	if err := renamePlaceholderSessionsTx(tx, courseID, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return w.Lecture(id)
}

func (w *WriteDB) SetLectureTranscript(id, path, text string, estimated int) (map[string]any, error) {
	if _, err := w.Lecture(id); err != nil {
		return nil, err
	}
	if _, err := w.db.Exec(
		"UPDATE lectures SET transcript_path=?,transcript_text=?,time_estimated=?,status='待校订' WHERE id=?",
		path, text, estimated, id,
	); err != nil {
		return nil, err
	}
	return w.Lecture(id)
}

func (w *WriteDB) SetLectureCourseware(id, path, text string) (map[string]any, error) {
	if _, err := w.Lecture(id); err != nil {
		return nil, err
	}
	if _, err := w.db.Exec(
		"UPDATE lectures SET ppt_path=?,ppt_text=?,ppt_processed=0 WHERE id=?",
		path, text, id,
	); err != nil {
		return nil, err
	}
	return w.Lecture(id)
}

func (w *WriteDB) ClearDerived(id string) error {
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"correction_checkpoints", "subtitles", "lecture_notes", "practice_sets"} {
		if _, err := tx.Exec("DELETE FROM "+table+" WHERE lecture_id=?", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (w *WriteDB) SaveQuestions(courseID string, questions []map[string]any) error {
	existing, err := queryAll(w.db, "SELECT id FROM past_questions WHERE course_id=?", courseID)
	if err != nil {
		return err
	}
	existingIDs := map[string]bool{}
	for _, row := range existing {
		existingIDs[ToStringValue(row["id"])] = true
	}
	incoming := map[string]bool{}
	for _, question := range questions {
		id := ToStringValue(question["id"])
		if ToStringValue(question["courseId"]) != courseID || !existingIDs[id] {
			return contract.InputInvalid("题目不属于当前课程")
		}
		incoming[id] = true
	}
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, row := range existing {
		id := ToStringValue(row["id"])
		if !incoming[id] {
			if _, err := tx.Exec("DELETE FROM past_questions WHERE id=? AND course_id=?", id, courseID); err != nil {
				return err
			}
		}
	}
	for _, question := range questions {
		encoded, err := json.Marshal(question)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			"UPDATE past_questions SET question_json=? WHERE id=? AND course_id=?",
			string(encoded), ToStringValue(question["id"]), courseID,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		"DELETE FROM practice_sets WHERE lecture_id IN (SELECT id FROM lectures WHERE course_id=?)",
		courseID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *WriteDB) Lecture(id string) (map[string]any, error) {
	lecture, err := queryOne(w.db, "SELECT * FROM lectures WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	if lecture == nil {
		return nil, contract.NewError(contract.CodeNotFound, "课次不存在")
	}
	return lecture, nil
}

func (w *WriteDB) CreateSession(courseID, title, lectureID string) (map[string]any, error) {
	tx, err := w.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	session, err := createSessionTx(tx, courseID, title, lectureID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return session, nil
}

func createSessionTx(tx *sql.Tx, courseID, title, lectureID string) (map[string]any, error) {
	course, err := queryOneTx(tx, "SELECT * FROM courses WHERE id=?", courseID)
	if err != nil {
		return nil, err
	}
	if course == nil {
		return nil, contract.NewError(contract.CodeNotFound, "课程不存在")
	}
	var lecture map[string]any
	if lectureID != "" {
		value, err := queryOneTx(tx, "SELECT * FROM lectures WHERE id=?", lectureID)
		if err != nil {
			return nil, err
		}
		if value == nil {
			return nil, contract.NewError(contract.CodeNotFound, "课次不存在")
		}
		if toStringValue(value["course_id"]) != courseID {
			return nil, contract.InputInvalid("课次不属于当前课程")
		}
		lecture = value
	} else {
		value, err := queryOneTx(tx, "SELECT * FROM lectures WHERE course_id=? ORDER BY created_at,id LIMIT 1", courseID)
		if err != nil {
			return nil, err
		}
		if value != nil {
			if toStringValue(value["course_id"]) != courseID {
				return nil, contract.InputInvalid("课次不属于当前课程")
			}
			lecture = value
		}
	}
	base := strings.TrimSpace(title)
	if base == "" && lecture != nil {
		base = toStringValue(lecture["title"])
	}
	if base == "" {
		base = "新对话"
	}
	name, err := sessionNameTx(tx, courseID, base, "")
	if err != nil {
		return nil, err
	}
	id := NewID()
	lectureRef := ""
	if lecture != nil {
		lectureRef = toStringValue(lecture["id"])
	}
	if _, err := tx.Exec(
		"INSERT INTO chat_sessions(id,course_id,title,lecture_id) VALUES(?,?,?,?)",
		id, courseID, name, nilIfEmpty(lectureRef),
	); err != nil {
		return nil, err
	}
	return queryOneTx(tx, "SELECT * FROM chat_sessions WHERE id=? AND course_id=?", id, courseID)
}

func sessionNameTx(tx *sql.Tx, courseID, base, ignoreID string) (string, error) {
	sessions, err := queryAllTx(tx, "SELECT * FROM chat_sessions WHERE course_id=? ORDER BY rowid", courseID)
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, session := range sessions {
		if ignoreID != "" && toStringValue(session["id"]) == ignoreID {
			continue
		}
		used[toStringValue(session["title"])] = true
	}
	if !used[base] {
		return base, nil
	}
	for number := 2; ; number++ {
		candidate := base + " · " + itoa(number)
		if !used[candidate] {
			return candidate, nil
		}
	}
}

func (w *WriteDB) DeleteCourse(id string) (bool, error) {
	if _, err := w.Course(id); err != nil {
		return false, err
	}
	if _, err := w.db.Exec("DELETE FROM courses WHERE id=?", id); err != nil {
		return false, err
	}
	if err := w.Checkpoint(); err != nil {
		return false, err
	}
	return true, nil
}

func (w *WriteDB) DeleteSession(courseID, id string) ([]map[string]any, error) {
	tx, err := w.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	session, err := queryOneTx(tx, "SELECT * FROM chat_sessions WHERE id=? AND course_id=?", id, courseID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, contract.InputInvalid("会话不属于当前课程")
	}
	if _, err := tx.Exec("DELETE FROM chat_sessions WHERE id=? AND course_id=?", id, courseID); err != nil {
		return nil, err
	}
	remaining, err := queryAllTx(tx, "SELECT * FROM chat_sessions WHERE course_id=? ORDER BY rowid", courseID)
	if err != nil {
		return nil, err
	}
	if len(remaining) == 0 {
		if _, err := createSessionTx(tx, courseID, "", ""); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := w.Checkpoint(); err != nil {
		return nil, err
	}
	return queryAll(w.db, "SELECT * FROM chat_sessions WHERE course_id=? ORDER BY rowid", courseID)
}

func (w *WriteDB) DeleteLecture(id string) (bool, error) {
	if _, err := w.Lecture(id); err != nil {
		return false, err
	}
	tx, err := w.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM messages WHERE lecture_id=?", id); err != nil {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM lectures WHERE id=?", id); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if err := w.Checkpoint(); err != nil {
		return false, err
	}
	return true, nil
}

func (w *WriteDB) AppendMessage(courseID string, lectureID *string, role, content string, sessionID *string) error {
	if role != "user" && role != "assistant" {
		return contract.InputInvalid("消息角色无效")
	}
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	course, err := queryOneTx(tx, "SELECT id FROM courses WHERE id=?", courseID)
	if err != nil {
		return err
	}
	if course == nil {
		return contract.NewError(contract.CodeNotFound, "课程不存在")
	}
	if lectureID != nil {
		lecture, err := queryOneTx(tx, "SELECT course_id FROM lectures WHERE id=?", *lectureID)
		if err != nil {
			return err
		}
		if lecture == nil {
			return contract.NewError(contract.CodeNotFound, "课次不存在")
		}
		if toStringValue(lecture["course_id"]) != courseID {
			return contract.InputInvalid("课次不属于当前课程")
		}
	}
	var session map[string]any
	if sessionID != nil {
		session, err = queryOneTx(tx, "SELECT * FROM chat_sessions WHERE id=? AND course_id=?", *sessionID, courseID)
		if err != nil {
			return err
		}
		if session == nil {
			return contract.InputInvalid("会话不属于当前课程")
		}
	} else {
		session, err = queryOneTx(tx, "SELECT * FROM chat_sessions WHERE course_id=? ORDER BY rowid LIMIT 1", courseID)
		if err != nil {
			return err
		}
		if session == nil {
			session, err = createSessionTx(tx, courseID, "", "")
			if err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(
		"INSERT INTO messages(id,course_id,lecture_id,role,content,session_id) VALUES(?,?,?,?,?,?)",
		ids.New(), courseID, lectureID, role, content, toStringValue(session["id"]),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *WriteDB) DeleteManagedFile(path string) error {
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := queryAllTx(tx, "SELECT id,course_id FROM course_files WHERE path=?", path)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := tx.Exec("DELETE FROM course_files WHERE id=? AND course_id=?",
			toStringValue(row["id"]), toStringValue(row["course_id"])); err != nil {
			return err
		}
		if _, err := tx.Exec(
			"DELETE FROM practice_sets WHERE lecture_id IN (SELECT id FROM lectures WHERE course_id=?)",
			toStringValue(row["course_id"]),
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("UPDATE lectures SET ppt_path='',ppt_text='' WHERE ppt_path=?", path); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE lectures SET transcript_path='',transcript_text='' WHERE transcript_path=?", path); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return w.Checkpoint()
}

func (w *WriteDB) StudyEntries() ([]map[string]any, error) {
	rows, err := queryAll(w.db, "SELECT json FROM study_entries ORDER BY rowid")
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for _, row := range rows {
		var entry map[string]any
		if err := json.Unmarshal([]byte(toStringValue(row["json"])), &entry); err != nil {
			return nil, contract.InputInvalid("课表记录损坏")
		}
		result = append(result, entry)
	}
	return result, nil
}

func (w *WriteDB) SaveStudyEntries(entries []map[string]any) error {
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM study_entries"); err != nil {
		return err
	}
	for _, entry := range entries {
		id := toStringValue(entry["id"])
		courseID := toStringValue(entry["courseId"])
		lectureID := toStringValue(entry["lectureId"])
		if lectureID != "" {
			lecture, err := queryOneTx(tx, "SELECT course_id,title FROM lectures WHERE id=?", lectureID)
			if err != nil {
				return err
			}
			if lecture == nil {
				return contract.NewError(contract.CodeNotFound, "课次不存在")
			}
			if toStringValue(lecture["course_id"]) != courseID {
				return contract.InputInvalid("课次不属于所选课程")
			}
			oldTitle := toStringValue(lecture["title"])
			if _, err := tx.Exec("UPDATE lectures SET title=? WHERE id=?", toStringValue(entry["title"]), lectureID); err != nil {
				return err
			}
			if _, err := tx.Exec(
				"UPDATE chat_sessions SET title=? WHERE lecture_id=? AND title=?",
				toStringValue(entry["title"]), lectureID, oldTitle,
			); err != nil {
				return err
			}
		} else if courseID != "" {
			course, err := queryOneTx(tx, "SELECT id FROM courses WHERE id=?", courseID)
			if err != nil {
				return err
			}
			if course == nil {
				return contract.NewError(contract.CodeNotFound, "课程不存在")
			}
		}
		encoded, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO study_entries(id,json) VALUES(?,?)", id, string(encoded)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (w *WriteDB) SetStatus(id, status string) error {
	if _, err := w.Lecture(id); err != nil {
		return err
	}
	_, err := w.db.Exec("UPDATE lectures SET status=? WHERE id=?", status, id)
	return err
}

func (w *WriteDB) SetLecturePptText(id, text string) error {
	_, err := w.db.Exec("UPDATE lectures SET ppt_text=? WHERE id=?", text, id)
	return err
}

func (w *WriteDB) UpdateLectureCorrection(id, transcriptText string, estimated int, pptText string) error {
	_, err := w.db.Exec(
		"UPDATE lectures SET transcript_text=?,time_estimated=?,ppt_text=? WHERE id=?",
		transcriptText, estimated, pptText, id,
	)
	return err
}

func (w *WriteDB) CorrectionCheckpoint(id string) (fingerprint string, total int, segmentsJSON string, err error) {
	row, err := queryOne(
		w.db,
		"SELECT fingerprint,total,segments_json FROM correction_checkpoints WHERE lecture_id=?",
		id,
	)
	if err != nil || row == nil {
		return "", 0, "", err
	}
	return ToStringValue(row["fingerprint"]), toIntValue(row["total"]), ToStringValue(row["segments_json"]), nil
}

func (w *WriteDB) SaveCorrectionBatch(id, fingerprint string, total int, segments any, completed int) error {
	if _, err := w.Lecture(id); err != nil {
		return err
	}
	encoded, err := json.Marshal(segments)
	if err != nil {
		return err
	}
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO correction_checkpoints(lecture_id,fingerprint,total,segments_json) VALUES(?,?,?,?)",
		id, fingerprint, total, string(encoded),
	); err != nil {
		return err
	}
	for _, statement := range []string{
		"DELETE FROM lecture_notes WHERE lecture_id=?",
		"DELETE FROM practice_sets WHERE lecture_id=?",
	} {
		if _, execErr := tx.Exec(statement, id); execErr != nil {
			return execErr
		}
	}
	if _, err := tx.Exec(
		"UPDATE lectures SET status=? WHERE id=?",
		fmt.Sprintf("校订中（已保存 %d/%d 条）", completed, total), id,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return w.Checkpoint()
}

func (w *WriteDB) SaveSubtitles(lectureID, srt string, segments any) error {
	if _, err := w.Lecture(lectureID); err != nil {
		return err
	}
	encoded, err := json.Marshal(segments)
	if err != nil {
		return err
	}
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO subtitles(lecture_id,srt_text,segments_json) VALUES(?,?,?)",
		lectureID, srt, string(encoded),
	); err != nil {
		return err
	}
	for _, statement := range []string{
		"DELETE FROM correction_checkpoints WHERE lecture_id=?",
		"DELETE FROM lecture_notes WHERE lecture_id=?",
		"DELETE FROM practice_sets WHERE lecture_id=?",
		"UPDATE lectures SET status='字幕完成' WHERE id=?",
	} {
		if _, err := tx.Exec(statement, lectureID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (w *WriteDB) ReplaceQuestions(courseID, fileID string, questions []map[string]any) error {
	file, err := queryOne(w.db, "SELECT course_id FROM course_files WHERE id=? AND course_id=?", fileID, courseID)
	if err != nil {
		return err
	}
	if file == nil {
		return contract.InputInvalid("资料不属于当前课程")
	}
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM past_questions WHERE course_id=? AND file_id=?", courseID, fileID); err != nil {
		return err
	}
	for _, question := range questions {
		encoded, err := json.Marshal(question)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			"INSERT INTO past_questions(id,course_id,file_id,question_json) VALUES(?,?,?,?)",
			toStringValue(question["id"]), courseID, fileID, string(encoded),
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(
		"DELETE FROM practice_sets WHERE lecture_id IN (SELECT id FROM lectures WHERE course_id=?)", courseID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *WriteDB) SavePractice(lectureID string, practice map[string]any) error {
	if _, err := w.Lecture(lectureID); err != nil {
		return err
	}
	pastIDs := []string{}
	if past, ok := practice["past"].([]any); ok {
		for _, item := range past {
			if question, ok := item.(map[string]any); ok {
				if id := toStringValue(question["id"]); id != "" {
					pastIDs = append(pastIDs, id)
				}
			}
		}
	}
	encodedIDs, err := json.Marshal(pastIDs)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(practice)
	if err != nil {
		return err
	}
	_, err = w.db.Exec(
		"INSERT OR REPLACE INTO practice_sets(lecture_id,past_ids_json,extended_json,updated_at) VALUES(?,?,?,CURRENT_TIMESTAMP)",
		lectureID, string(encodedIDs), string(encoded),
	)
	return err
}

func (w *WriteDB) SaveNotes(lectureID string, notes any) error {
	if _, err := w.Lecture(lectureID); err != nil {
		return err
	}
	encoded, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	tx, err := w.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT OR REPLACE INTO lecture_notes(lecture_id,notes_json) VALUES(?,?)", lectureID, string(encoded)); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM practice_sets WHERE lecture_id=?", lectureID); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *WriteDB) DeleteCorrectionCheckpoint(id string) error {
	_, err := w.db.Exec("DELETE FROM correction_checkpoints WHERE lecture_id=?", id)
	return err
}

func (w *WriteDB) CourseByName(name string) (map[string]any, error) {
	return queryOne(w.db, "SELECT * FROM courses WHERE name=? LIMIT 1", name)
}

func (w *WriteDB) LectureByTitle(courseID, title string) (map[string]any, error) {
	return queryOne(w.db, "SELECT * FROM lectures WHERE course_id=? AND title=? LIMIT 1", courseID, title)
}
