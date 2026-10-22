package api

import (
	"encoding/json"
	"path/filepath"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/courseware"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
)

type lectureWriteParams struct {
	ID             string           `json:"id"`
	CourseID       string           `json:"courseId"`
	Title          string           `json:"title"`
	TranscriptPath string           `json:"transcriptPath"`
	Path           string           `json:"path"`
	PptPath        string           `json:"pptPath"`
	Questions      []map[string]any `json:"questions"`
}

func resolveSource(state *contract.State, source string) (string, error) {
	if filepath.IsAbs(source) {
		return source, nil
	}
	return library.ManagedFile(state.LibraryRoot, source)
}

func (inv *Invoker) handleLectureCreate(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input lectureWriteParams
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.CourseID) {
		return nil, contract.InputInvalid("课程标识无效")
	}
	if len(input.Title) < 1 || len(input.Title) > 200 {
		return nil, contract.InputInvalid("课次标题无效")
	}
	if input.TranscriptPath == "" {
		return nil, contract.InputInvalid("原文路径无效")
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	source, err := resolveSource(state, input.TranscriptPath)
	if err != nil {
		return nil, err
	}
	original, err := documents.ReadTranscript(inv.native, source)
	if err != nil {
		return nil, err
	}
	pptPath := ""
	pptText := ""
	if input.PptPath != "" {
		ppt, err := resolveSource(state, input.PptPath)
		if err != nil {
			return nil, err
		}
		parsed, err := documents.ParseCourseware(inv.native, ppt)
		if err != nil {
			return nil, err
		}
		if parsed.NeedsOcr {
			return nil, contract.InputInvalid("课件是扫描 PDF，请使用 PPTX 或文字 PDF")
		}
		courseName, err := write.CourseName(input.CourseID)
		if err != nil {
			return nil, err
		}
		pptDirectory, err := library.CourseDirectory(state.LibraryRoot, "PPT", courseName)
		if err != nil {
			return nil, err
		}
		pptPath, err = copyInto(pptDirectory, ppt)
		if err != nil {
			return nil, err
		}
		serialized, err := courseware.SerializeSourceCache(pptPath, parsed.Text, "local", parsed.NeedsOcr, parsed.Omissions)
		if err != nil {
			return nil, err
		}
		pptText = serialized
	}
	lecture, err := write.CreateLecture(input.CourseID, input.Title, "", pptPath, pptText, "")
	if err != nil {
		return nil, err
	}
	courseName, err := write.CourseName(input.CourseID)
	if err != nil {
		return nil, err
	}
	transcriptDirectory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", courseName)
	if err != nil {
		return nil, err
	}
	transcriptCopy, err := copyInto(transcriptDirectory, source)
	if err != nil {
		return nil, err
	}
	return write.SetLectureTranscript(
		storage.ToStringValue(lecture["id"]),
		transcriptCopy,
		original.Text,
		boolToInt(original.Estimated),
	)
}

func (inv *Invoker) handleLectureTranscript(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input lectureWriteParams
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课次标识无效")
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	lecture, err := write.Lecture(input.ID)
	if err != nil {
		return nil, err
	}
	source, err := resolveSource(state, input.Path)
	if err != nil {
		return nil, err
	}
	original, err := documents.ReadTranscript(inv.native, source)
	if err != nil {
		return nil, err
	}
	courseName, err := write.CourseName(storage.ToStringValue(lecture["course_id"]))
	if err != nil {
		return nil, err
	}
	transcriptDirectory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", courseName)
	if err != nil {
		return nil, err
	}
	transcriptCopy, err := copyInto(transcriptDirectory, source)
	if err != nil {
		return nil, err
	}
	if _, err := write.SetLectureTranscript(input.ID, transcriptCopy, original.Text, boolToInt(original.Estimated)); err != nil {
		return nil, err
	}
	if err := write.ClearDerived(input.ID); err != nil {
		return nil, err
	}
	return inv.lectureDetailOf(state, input.ID)
}

func (inv *Invoker) handleLectureCourseware(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input lectureWriteParams
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.ID) {
		return nil, contract.InputInvalid("课次标识无效")
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	lecture, err := write.Lecture(input.ID)
	if err != nil {
		return nil, err
	}
	source, err := resolveSource(state, input.Path)
	if err != nil {
		return nil, err
	}
	parsed, err := documents.ParseCourseware(inv.native, source)
	if err != nil {
		return nil, err
	}
	if parsed.NeedsOcr {
		return nil, contract.InputInvalid("课件是扫描件，需要先 OCR 为文本 PDF")
	}
	courseName, err := write.CourseName(storage.ToStringValue(lecture["course_id"]))
	if err != nil {
		return nil, err
	}
	pptDirectory, err := library.CourseDirectory(state.LibraryRoot, "PPT", courseName)
	if err != nil {
		return nil, err
	}
	pptCopy, err := copyInto(pptDirectory, source)
	if err != nil {
		return nil, err
	}
	serialized, err := courseware.SerializeSourceCache(pptCopy, parsed.Text, "local", parsed.NeedsOcr, parsed.Omissions)
	if err != nil {
		return nil, err
	}
	if _, err := write.SetLectureCourseware(input.ID, pptCopy, serialized); err != nil {
		return nil, err
	}
	if err := write.ClearDerived(input.ID); err != nil {
		return nil, err
	}
	return inv.lectureDetailOf(state, input.ID)
}

func (inv *Invoker) handleQuestionsSave(_ contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	var input lectureWriteParams
	if err := json.Unmarshal(params, &input); err != nil || !config.IsUUID(input.CourseID) {
		return nil, contract.InputInvalid("课程标识无效")
	}
	if len(input.Questions) > 0 {
		for _, question := range input.Questions {
			if err := validateQuestion(question); err != nil {
				return nil, err
			}
		}
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	if err := write.SaveQuestions(input.CourseID, input.Questions); err != nil {
		return nil, err
	}
	if err := write.Checkpoint(); err != nil {
		return nil, err
	}
	return true, nil
}

func validateQuestion(question map[string]any) error {
	if storage.ToStringValue(question["id"]) == "" || len(storage.ToStringValue(question["id"])) > 200 ||
		storage.ToStringValue(question["sourceFile"]) == "" || storage.ToStringValue(question["type"]) == "" ||
		storage.ToStringValue(question["stem"]) == "" {
		return contract.InputInvalid("题目数据无效")
	}
	return nil
}
