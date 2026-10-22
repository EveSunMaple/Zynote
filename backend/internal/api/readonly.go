package api

import (
	"database/sql"
	"encoding/json"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
)

func openAssistantDB(state *contract.State) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(state.DataRoot, "assistant.db"))+"?mode=ro")
	if err != nil {
		return nil, contract.Unavailable("数据库无法打开")
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func rowsToMaps(rows *sql.Rows) ([]map[string]any, error) {
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for index, column := range columns {
			if bytes, ok := values[index].([]byte); ok {
				row[column] = string(bytes)
			} else {
				row[column] = values[index]
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func queryMaps(db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return rowsToMaps(rows)
}

func parseJSONArray(raw string) []any {
	var value []any
	if raw == "" || json.Unmarshal([]byte(raw), &value) != nil {
		return []any{}
	}
	return value
}

func handleCourseDetail(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课程标识无效")
	}
	db, err := openAssistantDB(state)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	courses, err := queryMaps(db, "SELECT * FROM courses WHERE id=?", input.ID)
	if err != nil {
		return nil, contract.Unavailable("课程读取失败")
	}
	if len(courses) != 1 {
		return nil, contract.NewError(contract.CodeNotFound, "课程不存在")
	}
	files, err := queryMaps(db, "SELECT * FROM course_files WHERE course_id=?", input.ID)
	if err != nil {
		return nil, contract.Unavailable("课程资料读取失败")
	}
	for _, file := range files {
		file["chunks"] = parseJSONArray(toString(file["chunks_json"]))
	}
	questions, err := queryMaps(db, "SELECT question_json FROM past_questions WHERE course_id=?", input.ID)
	if err != nil {
		return nil, contract.Unavailable("历年卷读取失败")
	}
	parsedQuestions := []any{}
	for _, question := range questions {
		var value any
		if json.Unmarshal([]byte(toString(question["question_json"])), &value) != nil {
			return nil, contract.InputInvalid("历年卷数据无效")
		}
		parsedQuestions = append(parsedQuestions, value)
	}
	lectures, err := queryMaps(db, "SELECT * FROM lectures WHERE course_id=? ORDER BY created_at,id", input.ID)
	if err != nil {
		return nil, contract.Unavailable("课次读取失败")
	}
	messages, err := queryMaps(db, "SELECT * FROM messages WHERE course_id=? ORDER BY rowid", input.ID)
	if err != nil {
		return nil, contract.Unavailable("消息读取失败")
	}
	sessions, err := queryMaps(db, "SELECT * FROM chat_sessions WHERE course_id=? ORDER BY rowid", input.ID)
	if err != nil {
		return nil, contract.Unavailable("会话读取失败")
	}
	return map[string]any{
		"course":    courses[0],
		"sessions":  sessions,
		"files":     files,
		"questions": parsedQuestions,
		"lectures":  lectures,
		"messages":  messages,
	}, nil
}

func handleLibraryList(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	db, err := openAssistantDB(state)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	courses, err := queryMaps(db, "SELECT id,name FROM courses ORDER BY created_at,id")
	if err != nil {
		return nil, contract.Unavailable("课程读取失败")
	}
	names := map[string]string{}
	for _, course := range courses {
		names[library.FolderName(toString(course["name"]))] = toString(course["name"])
	}
	files, err := library.ListLibrary(state.LibraryRoot)
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for _, file := range files {
		if courseName, ok := names[file["course"].(string)]; ok {
			file["course"] = courseName
		}
		result = append(result, file)
	}
	return result, nil
}

func toString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
