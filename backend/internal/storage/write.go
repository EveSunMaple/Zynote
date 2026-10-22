package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/contract"
)

type WriteDB struct {
	db *sql.DB
}

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

func newID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(bytes[0:4]),
		hex.EncodeToString(bytes[4:6]),
		hex.EncodeToString(bytes[6:8]),
		hex.EncodeToString(bytes[8:10]),
		hex.EncodeToString(bytes[10:16]))
}

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
	id := newID()
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
	id := newID()
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
