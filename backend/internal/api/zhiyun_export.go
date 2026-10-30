package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/courseware"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
	"github.com/EveSunMaple/Zynote/backend/internal/zhiyun"
)

const (
	maxZhiyunTranscriptBytes = 128 << 20
	maxZhiyunPPTBytes        = 512 << 20
)

var zhiyunFilenameInvalid = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func (inv *Invoker) findOrCreateZhiyunLecture(
	write *storage.WriteDB,
	courseID, title, planned string,
) (map[string]any, error) {
	if planned != "" {
		if lecture, err := write.Lecture(planned); err == nil {
			return lecture, nil
		}
	}
	if lecture, err := write.LectureByTitle(courseID, title); err == nil && lecture != nil {
		return lecture, nil
	}
	return write.CreateLecture(courseID, title, "", "", "", "")
}

func sanitizeZhiyunFilename(value string) string {
	name := zhiyunFilenameInvalid.ReplaceAllString(value, "_")
	if strings.TrimSpace(name) == "" {
		name = "智云导出"
	}
	return name
}

func readFileSignature(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	buffer := make([]byte, 12)
	count, err := io.ReadFull(file, buffer)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buffer[:count], nil
}

func zipSignature(value []byte) bool {
	return bytes.HasPrefix(value, []byte{'P', 'K', 3, 4})
}

func oleSignature(value []byte) bool {
	return bytes.HasPrefix(value, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
}

func validateOfficeArchive(path, kind string) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("下载文件不是有效的 Office 文档")
	}
	defer reader.Close()
	required := "ppt/presentation.xml"
	if kind == "transcript" {
		required = "word/document.xml"
	}
	for _, file := range reader.File {
		if file.Name == required {
			return nil
		}
	}
	if kind == "ppt" {
		return fmt.Errorf("智云返回的压缩包不是 PPTX")
	}
	return fmt.Errorf("智云返回的压缩包不是 DOCX")
}

func validateZhiyunDownload(path, kind, filename string) error {
	signature, err := readFileSignature(path)
	if err != nil {
		return fmt.Errorf("下载文件无法读取")
	}
	lower := strings.ToLower(filename)
	if kind == "ppt" {
		if zipSignature(signature) {
			return validateOfficeArchive(path, kind)
		}
		if oleSignature(signature) {
			return nil
		}
		return fmt.Errorf("智云返回的不是 PPT 文件（可能是视频或网页），已停止处理")
	}
	if strings.HasSuffix(lower, ".docx") {
		if zipSignature(signature) {
			return validateOfficeArchive(path, kind)
		}
		return fmt.Errorf("智云返回的不是 Word 原文文件（可能是视频或网页），已停止处理")
	}
	lowerSignature := bytes.ToLower(signature)
	if bytes.Contains(lowerSignature, []byte("<html")) || bytes.Contains(lowerSignature, []byte("ftyp")) {
		return fmt.Errorf("智云返回的不是语音识别原文文件（可能是视频或网页），已停止处理")
	}
	return nil
}

type zhiyunExportInput struct {
	Kind          string `json:"kind"`
	CourseID      string `json:"courseId"`
	SubID         string `json:"subId"`
	CourseName    string `json:"courseName"`
	LocalCourseID string `json:"localCourseId"`
	Title         string `json:"title"`
}

func parseZhiyunExportInput(params json.RawMessage) (zhiyunExportInput, error) {
	var input zhiyunExportInput
	if err := json.Unmarshal(params, &input); err != nil || (input.Kind != "transcript" && input.Kind != "ppt") ||
		!zhiyunDigits.MatchString(input.CourseID) || !zhiyunDigits.MatchString(input.SubID) {
		return input, contract.InputInvalid("智云导出参数无效")
	}
	return input, nil
}

func findZhiyunLesson(lessons []zhiyun.Lesson, subID string) (*zhiyun.Lesson, error) {
	for index := range lessons {
		if lessons[index].ID == subID {
			return &lessons[index], nil
		}
	}
	return nil, contract.InputInvalid("没有找到所选课次")
}

func ensureZhiyunCourse(write *storage.WriteDB, input zhiyunExportInput) (map[string]any, error) {
	if input.LocalCourseID != "" {
		if !config.IsUUID(input.LocalCourseID) {
			return nil, contract.InputInvalid("课程标识无效")
		}
		return write.Course(input.LocalCourseID)
	}
	name := strings.TrimSpace(input.CourseName)
	if name == "" {
		name = "智云课程 " + input.CourseID
	}
	if length := len([]rune(name)); length < 1 || length > 200 {
		return nil, contract.InputInvalid("课程名称无效")
	}
	if existing, _ := write.CourseByName(name); existing != nil {
		return existing, nil
	}
	return write.CreateCourse(name)
}

func zhiyunLectureMeta(state *contract.State, courseID string, input zhiyunExportInput, fallback string) (string, string) {
	title, planned := fallback, ""
	readDB, err := openReadOnlyDB(state)
	if err != nil {
		return title, planned
	}
	defer readDB.Close()
	rows, err := storage.QueryAll(readDB, "SELECT json FROM study_entries")
	if err != nil {
		return title, planned
	}
	for _, row := range rows {
		var entry map[string]any
		if json.Unmarshal([]byte(storage.ToStringValue(row["json"])), &entry) != nil {
			continue
		}
		if textValue(entry["courseId"]) != courseID || textValue(entry["zhiyunCourseId"]) != input.CourseID ||
			textValue(entry["subId"]) != input.SubID {
			continue
		}
		if value := textValue(entry["title"]); value != "" {
			title = value
		}
		return title, textValue(entry["lectureId"])
	}
	return title, planned
}

func formatTranscriptText(segments []zhiyun.TranscriptSegment) string {
	var builder strings.Builder
	for _, segment := range segments {
		hours := segment.Start / 3600
		minutes := (segment.Start / 60) % 60
		seconds := segment.Start % 60
		builder.WriteString(fmt.Sprintf("[%02d:%02d:%02d] %s\n", hours, minutes, seconds, segment.Text))
	}
	return builder.String()
}

func (inv *Invoker) saveZhiyunTranscriptText(state *contract.State, lectureID, courseID, courseName, title string,
	segments []zhiyun.TranscriptSegment) (any, error) {
	directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", courseName)
	if err != nil {
		return nil, err
	}
	text := formatTranscriptText(segments)
	if strings.TrimSpace(text) == "" {
		return nil, contract.InputInvalid("该课次暂无语音识别原文")
	}
	path, err := library.ReserveFile(directory, sanitizeZhiyunFilename(title)+".txt")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return nil, contract.Unavailable("语音识别原文写入失败")
	}
	write, err := openWriteDB(state)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	defer write.Close()
	if _, err := write.SetLectureTranscript(lectureID, path, text, 0); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	_ = write.DeleteCorrectionCheckpoint(lectureID)
	return map[string]any{
		"path": path, "lectureId": lectureID, "courseId": courseID,
		"warning": "已根据智云课堂的语音识别记录生成文本原文（.txt）",
	}, nil
}

func (inv *Invoker) exportZhiyunTranscript(ctx context.Context, client *zhiyun.Client, // NOSONAR: action contract groups related parameters; changing the signature would break the protocol.
	state *contract.State, input zhiyunExportInput, lesson *zhiyun.Lesson, lectureID, courseID, courseName string) (any, error) {
	if client.Progress != nil {
		client.Progress("正在读取课堂语音识别记录…")
	}
	segments, transcriptErr := client.Transcript(ctx, input.SubID)
	if transcriptErr == nil && len(segments) > 0 {
		return inv.saveZhiyunTranscriptText(state, lectureID, courseID, courseName, lesson.Title, segments)
	}
	if client.Progress != nil {
		client.Progress("课堂语音识别记录不可用，尝试导出 Word 原文…")
	}
	directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", courseName)
	if err != nil {
		return nil, err
	}
	resource, err := client.Resource(ctx, input.CourseID, input.SubID, "transcript")
	if err != nil {
		if transcriptErr != nil {
			return nil, contract.Unavailable(transcriptErr.Error())
		}
		return nil, contract.Unavailable(err.Error())
	}
	path, err := library.ReserveFile(directory, sanitizeZhiyunFilename(resource.Filename))
	if err != nil {
		return nil, err
	}
	if err := client.DownloadLimited(ctx, resource.URL, path, maxZhiyunTranscriptBytes); err != nil {
		_ = os.Remove(path)
		return nil, contract.Unavailable(err.Error())
	}
	if err := validateZhiyunDownload(path, "transcript", resource.Filename); err != nil {
		_ = os.Remove(path)
		return nil, contract.InputInvalid(err.Error())
	}
	if client.Progress != nil {
		client.Progress("正在解析语音识别原文…")
	}
	transcript, err := documents.ReadTranscript(inv.native, path)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	write, err := openWriteDB(state)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	defer write.Close()
	if _, err := write.SetLectureTranscript(lectureID, path, transcript.Text, boolToInt(transcript.Estimated)); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	_ = write.DeleteCorrectionCheckpoint(lectureID)
	return map[string]any{"path": path, "lectureId": lectureID, "courseId": courseID}, nil
}

func (inv *Invoker) saveZhiyunPPT(state *contract.State, lectureID, downloaded, courseID string) (any, error) {
	text := ""
	needsOcr := false
	omissions := []string{}
	warning := ""
	parsed, err := documents.ParseCourseware(inv.native, downloaded)
	if err != nil {
		needsOcr = true
		warning = "PPT 已下载，但无法提取文字：" + err.Error()
		omissions = append(omissions, "课件文字未能提取，请直接打开原文件")
	} else {
		text = parsed.Text
		needsOcr = parsed.NeedsOcr
		omissions = append(omissions, parsed.Omissions...)
		if strings.TrimSpace(text) == "" {
			needsOcr = true
			warning = "PPT 已下载，但未提取到文字；可能需要 OCR 或直接查看原文件"
			omissions = append(omissions, "课件文字为空，请直接打开原文件")
		}
	}
	serialized, err := courseware.SerializeSourceCache(downloaded, text, "local", needsOcr, omissions)
	if err != nil {
		_ = os.Remove(downloaded)
		return nil, err
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	defer write.Close()
	if _, err := write.SetLectureCourseware(lectureID, downloaded, serialized); err != nil {
		return nil, err
	}
	_ = write.DeleteCorrectionCheckpoint(lectureID)
	return map[string]any{
		"path": downloaded, "lectureId": lectureID, "courseId": courseID, "warning": warning,
	}, nil
}

func (inv *Invoker) exportZhiyunPPT(ctx context.Context, state *contract.State, // NOSONAR: action contract groups related parameters; changing the signature would break the protocol.
	client *zhiyun.Client, input zhiyunExportInput, lectureID, courseID, courseName string) (any, error) {
	// Never substitute a course video for a PPT.  The user asked for the PPT
	// resource, and silently downloading a multi-gigabyte video made the task
	// center report a failure for a file nobody requested.
	pptDirectory, err := library.CourseDirectory(state.LibraryRoot, "PPT", courseName)
	if err != nil {
		return nil, err
	}
	resource, err := client.Resource(ctx, input.CourseID, input.SubID, "ppt")
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	downloaded, err := library.ReserveFile(pptDirectory, sanitizeZhiyunFilename(resource.Filename))
	if err != nil {
		return nil, err
	}
	if err := client.DownloadLimited(ctx, resource.URL, downloaded, maxZhiyunPPTBytes); err != nil {
		_ = os.Remove(downloaded)
		return nil, contract.Unavailable(err.Error())
	}
	if err := validateZhiyunDownload(downloaded, "ppt", resource.Filename); err != nil {
		_ = os.Remove(downloaded)
		return nil, contract.InputInvalid(err.Error())
	}
	if client.Progress != nil {
		client.Progress("正在解析 PPT…")
	}
	return inv.saveZhiyunPPT(state, lectureID, downloaded, courseID)
}

func (inv *Invoker) exportZhiyun(ctx context.Context, store contract.StateStore, state *contract.State, input zhiyunExportInput, // NOSONAR: action contract groups related parameters; changing the signature would break the protocol.
	progress func(string), ready func(courseID, lectureID string)) (any, error) {
	client, err := inv.zhiyunClient(store)
	if err != nil {
		return nil, err
	}
	client.Progress = func(message string) {
		if progress != nil {
			progress(message)
		}
		inv.emit(map[string]any{"kind": "progress", "data": map[string]any{
			"stage": "智云课堂", "message": message,
		}})
	}
	lessons, err := client.Lessons(ctx, input.CourseID)
	if err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	lesson, err := findZhiyunLesson(lessons, input.SubID)
	if err != nil {
		return nil, err
	}
	write, err := openWriteDB(state)
	if err != nil {
		return nil, err
	}
	course, err := ensureZhiyunCourse(write, input)
	if err != nil {
		write.Close()
		return nil, err
	}
	courseID := storage.ToStringValue(course["id"])
	courseName := storage.ToStringValue(course["name"])
	if err := ensureCourseLibrary(state.LibraryRoot, []string{courseName}); err != nil {
		write.Close()
		return nil, err
	}
	title, planned := zhiyunLectureMeta(state, courseID, input, lesson.Title)
	lecture, err := inv.findOrCreateZhiyunLecture(write, courseID, title, planned)
	if err != nil {
		write.Close()
		return nil, err
	}
	lectureID := storage.ToStringValue(lecture["id"])
	// Release the write connection before the long download.  Keeping it open
	// would block every other database write while a large file is fetched.
	if err := write.Close(); err != nil {
		return nil, err
	}
	if ready != nil {
		ready(courseID, lectureID)
	}
	if input.Kind == "transcript" {
		return inv.exportZhiyunTranscript(ctx, client, state, input, lesson, lectureID, courseID, courseName)
	}
	return inv.exportZhiyunPPT(ctx, state, client, input, lectureID, courseID, courseName)
}

func (inv *Invoker) handleZhiyunExport(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	input, err := parseZhiyunExportInput(params)
	if err != nil {
		return nil, err
	}
	return inv.startZhiyunTask(store, state, input, zhiyunTaskTitle(input))
}
