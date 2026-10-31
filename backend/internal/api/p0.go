package api

import (
	"database/sql"
	"encoding/json"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

const assistantDBFile = "assistant.db"

func handleAppInfo(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return map[string]any{
		"version":  state.AppVersion,
		"electron": state.ElectronVersion,
		"platform": state.Platform,
	}, nil
}

func (inv *Invoker) callNative(state *contract.State, action string, params json.RawMessage) (any, error) {
	if inv.native == nil {
		return nil, contract.Unavailable("原生助手不可用")
	}
	var input struct {
		Path string  `json:"path"`
		Page *uint64 `json:"page"`
	}
	if err := json.Unmarshal(params, &input); err != nil || input.Path == "" {
		return nil, contract.InputInvalid("PDF 路径无效")
	}
	payload := map[string]any{"path": input.Path}
	if action == "pdf.text" && input.Page != nil {
		payload["page"] = *input.Page
	}
	result, err := inv.native.Call(action, payload, 15000)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	var value any
	if err := json.Unmarshal(result, &value); err != nil {
		return nil, contract.Unavailable("原生助手返回无效")
	}
	return value, nil
}

func handleSettingsGet(store contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return config.ReadSettings(state.ConfigRoot, state.PromptsRoot, store.Reverse())
}

func handleAppearanceGet(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return config.ReadAppearance(state.ConfigRoot)
}

func handleIdentityGet(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return config.ReadIdentity(state.ConfigRoot)
}

func handleDBHealth(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	return storage.Health(filepath.Join(state.DataRoot, assistantDBFile))
}

func handleDBBackup(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Target string `json:"target"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &input); err != nil {
			return nil, contract.InputInvalid("备份参数无效")
		}
	}
	return storage.Backup(filepath.Join(state.DataRoot, assistantDBFile), input.Target)
}

func openReadOnlyDB(state *contract.State) (*sql.DB, error) {
	// 只读连接也要设置 busy_timeout：WAL 数据库在最后一个连接关闭后会删除
	// -shm/-wal，多个只读请求同时打开时需要等对方完成共享内存初始化，否则会
	// 偶发 SQLITE_BUSY（database is locked）。
	dsn := "file:" + filepath.ToSlash(filepath.Join(state.DataRoot, assistantDBFile)) +
		"?mode=ro&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, contract.Unavailable("数据库无法打开")
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func handleCourseDetail(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课程标识无效")
	}
	db, err := openReadOnlyDB(state)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	courses, err := storage.QueryAll(db, "SELECT * FROM courses WHERE id=?", input.ID)
	if err != nil {
		return nil, contract.Unavailable("课程读取失败")
	}
	if len(courses) != 1 {
		return nil, contract.NewError(contract.CodeNotFound, "课程不存在")
	}
	files, err := storage.QueryAll(db, "SELECT * FROM course_files WHERE course_id=?", input.ID)
	if err != nil {
		return nil, contract.Unavailable("课程资料读取失败")
	}
	for _, file := range files {
		file["chunks"] = parseJSONArray(storage.ToStringValue(file["chunks_json"]))
	}
	questions, err := storage.QueryAll(db, "SELECT question_json FROM past_questions WHERE course_id=?", input.ID)
	if err != nil {
		return nil, contract.Unavailable("历年卷读取失败")
	}
	parsedQuestions := []any{}
	for _, question := range questions {
		var value any
		if json.Unmarshal([]byte(storage.ToStringValue(question["question_json"])), &value) != nil {
			return nil, contract.InputInvalid("历年卷数据无效")
		}
		parsedQuestions = append(parsedQuestions, value)
	}
	lectures, err := storage.QueryAll(db, "SELECT * FROM lectures WHERE course_id=? ORDER BY created_at,id", input.ID)
	if err != nil {
		return nil, contract.Unavailable("课次读取失败")
	}
	messages, err := storage.QueryAll(db, "SELECT * FROM messages WHERE course_id=? ORDER BY rowid", input.ID)
	if err != nil {
		return nil, contract.Unavailable("消息读取失败")
	}
	sessions, err := storage.QueryAll(db, "SELECT * FROM chat_sessions WHERE course_id=? ORDER BY rowid", input.ID)
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
	db, err := openReadOnlyDB(state)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	courses, err := storage.QueryAll(db, "SELECT id,name FROM courses ORDER BY created_at,id")
	if err != nil {
		return nil, contract.Unavailable("课程读取失败")
	}
	names := map[string]string{}
	for _, course := range courses {
		names[library.FolderName(storage.ToStringValue(course["name"]))] = storage.ToStringValue(course["name"])
	}
	files, err := library.ListLibrary(state.LibraryRoot)
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for _, file := range files {
		if courseName, ok := names[storage.ToStringValue(file["course"])]; ok {
			file["course"] = courseName
		}
		result = append(result, file)
	}
	return result, nil
}

func parseJSONArray(raw string) []any {
	var value []any
	if raw == "" || json.Unmarshal([]byte(raw), &value) != nil {
		return []any{}
	}
	return value
}
