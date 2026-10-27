package storage

import (
	"database/sql"
	"errors"
	"regexp"
)

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS courses(id TEXT PRIMARY KEY,name TEXT NOT NULL,created_at TEXT DEFAULT CURRENT_TIMESTAMP)`,
	`CREATE TABLE IF NOT EXISTS study_entries(id TEXT PRIMARY KEY,json TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS course_files(id TEXT PRIMARY KEY,course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,kind TEXT NOT NULL,filename TEXT,path TEXT,text TEXT,chunks_json TEXT,needs_ocr INTEGER DEFAULT 0)`,
	`CREATE TABLE IF NOT EXISTS past_questions(id TEXT PRIMARY KEY,course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,file_id TEXT REFERENCES course_files(id) ON DELETE CASCADE,question_json TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS lectures(id TEXT PRIMARY KEY,course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,title TEXT,video_path TEXT,ppt_path TEXT,ppt_text TEXT,status TEXT DEFAULT '待转写',created_at TEXT DEFAULT CURRENT_TIMESTAMP)`,
	`CREATE TABLE IF NOT EXISTS subtitles(lecture_id TEXT PRIMARY KEY REFERENCES lectures(id) ON DELETE CASCADE,srt_text TEXT,segments_json TEXT)`,
	`CREATE TABLE IF NOT EXISTS correction_checkpoints(lecture_id TEXT PRIMARY KEY REFERENCES lectures(id) ON DELETE CASCADE,fingerprint TEXT NOT NULL,total INTEGER NOT NULL,segments_json TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS lecture_notes(lecture_id TEXT PRIMARY KEY REFERENCES lectures(id) ON DELETE CASCADE,notes_json TEXT,updated_at TEXT DEFAULT CURRENT_TIMESTAMP)`,
	`CREATE TABLE IF NOT EXISTS practice_sets(lecture_id TEXT PRIMARY KEY REFERENCES lectures(id) ON DELETE CASCADE,past_ids_json TEXT,extended_json TEXT,updated_at TEXT DEFAULT CURRENT_TIMESTAMP)`,
	`CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,lecture_id TEXT REFERENCES lectures(id) ON DELETE CASCADE,role TEXT,content TEXT,created_at TEXT DEFAULT CURRENT_TIMESTAMP)`,
	`CREATE TABLE IF NOT EXISTS chat_sessions(id TEXT PRIMARY KEY,course_id TEXT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,title TEXT NOT NULL,created_at TEXT DEFAULT CURRENT_TIMESTAMP)`,
}

var bootstrapColumns = map[string]map[string]string{
	"lectures": {
		"source_video_path": "TEXT NOT NULL DEFAULT ''",
		"transcript_path":   "TEXT NOT NULL DEFAULT ''",
		"transcript_text":   "TEXT NOT NULL DEFAULT ''",
		"time_estimated":    "INTEGER NOT NULL DEFAULT 0",
		"ppt_processed":     "INTEGER NOT NULL DEFAULT 0",
	},
	"chat_sessions": {"lecture_id": "TEXT REFERENCES lectures(id) ON DELETE SET NULL"},
	"messages":      {"session_id": "TEXT REFERENCES chat_sessions(id) ON DELETE CASCADE"},
}

var lecturesColumns = []string{
	"source_video_path",
	"transcript_path",
	"transcript_text",
	"time_estimated",
	"ppt_processed",
}

func Bootstrap(db *sql.DB) error {
	for _, statement := range schemaStatements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	for _, column := range lecturesColumns {
		if err := addColumnIfMissing(db, "lectures", column); err != nil {
			return err
		}
	}
	if err := addColumnIfMissing(db, "chat_sessions", "lecture_id"); err != nil {
		return err
	}
	if err := addColumnIfMissing(db, "messages", "session_id"); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	courses, err := queryAllTx(tx, "SELECT id FROM courses ORDER BY created_at,id")
	if err != nil {
		return err
	}
	for _, course := range courses {
		courseID := toStringValue(course["id"])
		session, err := queryOneTx(tx, "SELECT * FROM chat_sessions WHERE course_id=? ORDER BY rowid LIMIT 1", courseID)
		if err != nil {
			return err
		}
		if session == nil {
			sessionID := NewID()
			if _, err := tx.Exec("INSERT INTO chat_sessions(id,course_id,title) VALUES(?,?,?)", sessionID, courseID, "新对话"); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE messages SET session_id=? WHERE course_id=? AND session_id IS NULL", sessionID, courseID); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(
			"UPDATE messages SET session_id=? WHERE course_id=? AND session_id IS NULL",
			toStringValue(session["id"]), courseID,
		); err != nil {
			return err
		}
		if err := renamePlaceholderSessionsTx(tx, courseID, ""); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func addColumnIfMissing(db *sql.DB, table, column string) error {
	declaration, ok := bootstrapColumns[table][column]
	if !ok {
		return errors.New("未知的数据库迁移列")
	}
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec("ALTER TABLE " + table + " ADD COLUMN " + column + " " + declaration) // NOSONAR: table, column and declaration are all checked against the fixed migration allowlist above.
	return err
}

var placeholderPattern = regexp.MustCompile(`^(会话 \d+|新对话(?: · \d+)?)$`)

func renamePlaceholderSessionsTx(tx *sql.Tx, courseID, lectureID string) error {
	var preferred map[string]any
	if lectureID != "" {
		lecture, err := queryOneTx(tx, "SELECT * FROM lectures WHERE id=?", lectureID)
		if err != nil {
			return err
		}
		if lecture != nil {
			if toStringValue(lecture["course_id"]) != courseID {
				return sql.ErrNoRows
			}
			preferred = lecture
		}
	}
	sessions, err := queryAllTx(tx, "SELECT * FROM chat_sessions WHERE course_id=? ORDER BY rowid", courseID)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		title := toStringValue(session["title"])
		if !placeholderPattern.MatchString(title) {
			continue
		}
		var row map[string]any
		if preferred != nil {
			row = preferred
		} else {
			row, err = queryOneTx(
				tx,
				"SELECT l.* FROM messages m JOIN lectures l ON l.id=m.lecture_id WHERE m.session_id=? ORDER BY m.rowid LIMIT 1",
				toStringValue(session["id"]),
			)
			if err != nil {
				return err
			}
			if row == nil {
				row, err = queryOneTx(
					tx,
					"SELECT * FROM lectures WHERE course_id=? ORDER BY created_at,id LIMIT 1",
					courseID,
				)
				if err != nil {
					return err
				}
			}
		}
		base := "新对话"
		if row != nil {
			if value := toStringValue(row["title"]); value != "" {
				base = value
			}
		}
		used := map[string]bool{}
		for _, other := range sessions {
			if toStringValue(other["id"]) == toStringValue(session["id"]) {
				continue
			}
			used[toStringValue(other["title"])] = true
		}
		name := base
		if used[name] {
			for number := 2; ; number++ {
				candidate := base + " · " + itoa(number)
				if !used[candidate] {
					name = candidate
					break
				}
			}
		}
		lectureRef := ""
		if row != nil {
			lectureRef = toStringValue(row["id"])
		}
		if _, err := tx.Exec(
			"UPDATE chat_sessions SET title=?,lecture_id=? WHERE id=?",
			name, nilIfEmpty(lectureRef),
			toStringValue(session["id"]),
		); err != nil {
			return err
		}
	}
	return nil
}
