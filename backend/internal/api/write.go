package api

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

func openWriteDB(state *contract.State) (*storage.WriteDB, error) {
	write, err := storage.OpenWrite(filepath.Join(state.DataRoot, "assistant.db"))
	if err != nil {
		return nil, contract.Unavailable("数据库无法打开")
	}
	return write, nil
}

func prepareDatabase(state *contract.State) error {
	_, err := storage.Prepare(
		filepath.Join(state.DataRoot, "assistant.db"),
		filepath.Join(state.DataRoot, "Backups"),
	)
	return err
}

func handleCourseCreate(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(params, &input); err != nil || len(input.Name) < 1 || len(input.Name) > 200 {
		return nil, contract.InputInvalid("课程名称无效")
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	course, err := write.CreateCourse(input.Name)
	if err != nil {
		return nil, err
	}
	if err := ensureCourseLibrary(state.LibraryRoot, []string{storage.ToStringValue(course["name"])}); err != nil {
		return nil, err
	}
	return course, nil
}

func handleSessions(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	return write.Sessions()
}

func handleSessionCreate(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID  string  `json:"courseId"`
		LectureID *string `json:"lectureId"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.CourseID) {
		return nil, contract.InputInvalid("课程标识无效")
	}
	lectureID := ""
	if input.LectureID != nil {
		if !config.IsUUID(*input.LectureID) {
			return nil, contract.InputInvalid("课次标识无效")
		}
		lectureID = *input.LectureID
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	return write.CreateSession(input.CourseID, "", lectureID)
}

func ensureCourseLibrary(root string, courses []string) error {
	for _, category := range library.Categories {
		if err := os.MkdirAll(filepath.Join(root, category), 0o755); err != nil {
			return contract.Unavailable("资料目录不可创建")
		}
		for _, course := range courses {
			if err := os.MkdirAll(filepath.Join(root, category, library.FolderName(course)), 0o755); err != nil {
				return contract.Unavailable("资料目录不可创建")
			}
		}
	}
	return nil
}

func handleDBBootstrap(_ contract.StateStore, state *contract.State, _ json.RawMessage) (any, error) {
	if err := prepareDatabase(state); err != nil {
		return nil, err
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	if err := storage.BootstrapInto(write); err != nil {
		return nil, contract.Unavailable("数据库初始化失败: " + err.Error())
	}
	if err := write.Checkpoint(); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func confirmedDelete(params json.RawMessage, subject string) error {
	var input struct {
		Confirmed bool `json:"confirmed"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !input.Confirmed {
		return contract.InputInvalid("请先确认删除" + subject)
	}
	return nil
}

func handleCourseDelete(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课程标识无效")
	}
	if err := confirmedDelete(params, "课程"); err != nil {
		return nil, err
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	return write.DeleteCourse(input.ID)
}

func handleSessionDelete(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID string `json:"courseId"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.CourseID) || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("标识无效")
	}
	if err := confirmedDelete(params, "会话"); err != nil {
		return nil, err
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	return write.DeleteSession(input.CourseID, input.ID)
}

func handleLectureDelete(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课次标识无效")
	}
	if err := confirmedDelete(params, "课次"); err != nil {
		return nil, err
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	return write.DeleteLecture(input.ID)
}
