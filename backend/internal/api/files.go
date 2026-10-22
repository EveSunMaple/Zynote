package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/courseware"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/ids"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
)

func (inv *Invoker) handleFilesAdd(state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID string   `json:"courseId"`
		Paths    []string `json:"paths"`
		Kind     string   `json:"kind"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.CourseID) {
		return nil, contract.InputInvalid("课程标识无效")
	}
	if input.Kind != "senior_notes" && input.Kind != "past_exam" {
		return nil, contract.InputInvalid("资料类型无效")
	}
	if len(input.Paths) == 0 || len(input.Paths) > 50 {
		return nil, contract.InputInvalid("资料路径无效")
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	if _, err := write.Course(input.CourseID); err != nil {
		return nil, err
	}
	courseName, err := write.CourseName(input.CourseID)
	if err != nil {
		return nil, err
	}
	category := "Notes"
	if input.Kind == "past_exam" {
		category = "PastExams"
	}
	directory, err := library.CourseDirectory(state.LibraryRoot, category, courseName)
	if err != nil {
		return nil, err
	}
	added := []map[string]any{}
	for _, path := range input.Paths {
		source, err := library.ManagedFile(state.LibraryRoot, path)
		if err != nil {
			return nil, err
		}
		parsed, err := documents.ParseCourseware(inv.native, source)
		if err != nil {
			return nil, err
		}
		destination, err := copyInto(directory, source)
		if err != nil {
			return nil, err
		}
		file := map[string]any{
			"id":          ids.New(),
			"course_id":   input.CourseID,
			"kind":        input.Kind,
			"filename":    filepath.Base(source),
			"path":        destination,
			"text":        parsed.Text,
			"chunks_json": mustJSON(courseware.Chunks(parsed.Text, 14000)),
			"needs_ocr":   boolToInt(parsed.NeedsOcr),
		}
		if err := write.InsertFile(file); err != nil {
			return nil, err
		}
		added = append(added, file)
	}
	return added, nil
}

func (inv *Invoker) handleFilesRemove(state *contract.State, params json.RawMessage) (any, error) {
	var input struct {
		CourseID string `json:"courseId"`
		ID       string `json:"id"`
	}
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.CourseID) || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("标识无效")
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	if err := write.RemoveFile(input.CourseID, input.ID); err != nil {
		return nil, err
	}
	if err := write.Checkpoint(); err != nil {
		return nil, err
	}
	return true, nil
}

func copyInto(directory, source string) (string, error) {
	extension := filepath.Ext(source)
	name := strings.TrimSuffix(filepath.Base(source), extension)
	destination := filepath.Join(directory, name+"-"+ids.New()+strings.ToLower(extension))
	raw, err := os.ReadFile(source)
	if err != nil {
		return "", contract.Unavailable("资料复制失败")
	}
	if err := os.WriteFile(destination, raw, 0o644); err != nil {
		return "", contract.Unavailable("资料复制失败")
	}
	return destination, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
