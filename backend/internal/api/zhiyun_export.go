package api

import (
	"context"
	"encoding/json"
	"image"
	// Register the JPEG decoder for image.Decode.
	_ "image/jpeg"
	// Register the PNG decoder for image.Decode.
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/EveSunMaple/Zynote/backend/internal/config"
	"github.com/EveSunMaple/Zynote/backend/internal/contract"
	"github.com/EveSunMaple/Zynote/backend/internal/courseware"
	"github.com/EveSunMaple/Zynote/backend/internal/documents"
	"github.com/EveSunMaple/Zynote/backend/internal/library"
	"github.com/EveSunMaple/Zynote/backend/internal/pptextract"
	"github.com/EveSunMaple/Zynote/backend/internal/storage"
	"github.com/EveSunMaple/Zynote/backend/internal/videoppt"
	"github.com/EveSunMaple/Zynote/backend/internal/zhiyun"
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

var zhiyunDeckFallbackPattern = regexp.MustCompile(`纯图片型|编辑对象|旧版 PPT|分辨率|200 MB|500 页|多张图片`)

func scanVisiblePixels(value image.Image) bool {
	bounds := value.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			red, green, blue, _ := value.At(x, y).RGBA()
			if red>>8 >= 42 || green>>8 >= 42 || blue>>8 >= 42 {
				return true
			}
		}
	}
	return false
}

func pageHasVisiblePixels(path string) (bool, bool) {
	file, err := os.Open(path)
	if err != nil {
		return false, false
	}
	imageValue, _, decodeErr := image.Decode(file)
	_ = file.Close()
	if decodeErr != nil {
		return false, false
	}
	return scanVisiblePixels(imageValue), true
}

func zhiyunDeckAllBlack(path string) bool {
	directory, err := os.MkdirTemp("", "zynote-zhiyun-deck-*")
	if err != nil {
		return true
	}
	defer os.RemoveAll(directory)
	deck, err := pptextract.ReadDeck(path, directory)
	if err != nil {
		return !zhiyunDeckFallbackPattern.MatchString(err.Error())
	}
	for _, page := range deck.Pages {
		if page.Unavailable != "" {
			continue
		}
		visible, ok := pageHasVisiblePixels(page.ImagePath)
		if ok && visible {
			return false
		}
	}
	return true
}

func sanitizeZhiyunFilename(value string) string {
	name := zhiyunFilenameInvalid.ReplaceAllString(value, "_")
	if strings.TrimSpace(name) == "" {
		name = "智云导出"
	}
	return name
}

func zhiyunFallbackError(err error) bool {
	message := err.Error()
	for _, marker := range []string{"暂无可导出", "未能生成PPT", "PPT生成超时", "文件格式暂不支持"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

type zhiyunExportInput struct {
	Kind          string `json:"kind"`
	CourseID      string `json:"courseId"`
	SubID         string `json:"subId"`
	CourseName    string `json:"courseName"`
	LocalCourseID string `json:"localCourseId"`
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

func (inv *Invoker) persistZhiyunCourseware(write *storage.WriteDB, lectureID, path string) error {
	parsed, err := documents.ParseCourseware(inv.native, path)
	if err != nil {
		return err
	}
	serialized, err := courseware.SerializeSourceCache(path, parsed.Text, "local", parsed.NeedsOcr, parsed.Omissions)
	if err != nil {
		return err
	}
	_, err = write.SetLectureCourseware(lectureID, path, serialized)
	return err
}

func (inv *Invoker) exportZhiyunTranscript(ctx context.Context, client *zhiyun.Client, write *storage.WriteDB, // NOSONAR: action contract groups related parameters; changing the signature would break the protocol.
	state *contract.State, input zhiyunExportInput, lesson *zhiyun.Lesson, lectureID, courseID, courseName string) (any, error) {
	directory, err := library.CourseDirectory(state.LibraryRoot, "Transcripts", courseName)
	if err != nil {
		return nil, err
	}
	path, err := library.ReserveFile(directory, sanitizeZhiyunFilename(lesson.Title)+".docx")
	if err != nil {
		return nil, err
	}
	resource, err := client.Resource(ctx, input.CourseID, input.SubID, "transcript")
	if err != nil {
		_ = os.Remove(path)
		return nil, contract.Unavailable(err.Error())
	}
	if err := client.Download(ctx, resource.URL, path); err != nil {
		_ = os.Remove(path)
		return nil, contract.Unavailable(err.Error())
	}
	transcript, err := documents.ReadTranscript(inv.native, path)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	if _, err := write.SetLectureTranscript(lectureID, path, transcript.Text, boolToInt(transcript.Estimated)); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	_ = write.DeleteCorrectionCheckpoint(lectureID)
	return map[string]any{"path": path, "lectureId": lectureID, "courseId": courseID}, nil
}

func (inv *Invoker) tryZhiyunPPT(write *storage.WriteDB, lectureID, downloaded, courseID string) (any, bool, error) {
	fallback := strings.ToLower(filepath.Ext(downloaded)) == ".pptx" && zhiyunDeckAllBlack(downloaded)
	if fallback {
		return nil, false, nil
	}
	parsed, err := documents.ParseCourseware(inv.native, downloaded)
	if err != nil || strings.TrimSpace(parsed.Text) == "" {
		return nil, false, nil
	}
	serialized, err := courseware.SerializeSourceCache(downloaded, parsed.Text, "local", parsed.NeedsOcr, parsed.Omissions)
	if err != nil {
		_ = os.Remove(downloaded)
		return nil, false, err
	}
	if _, err := write.SetLectureCourseware(lectureID, downloaded, serialized); err != nil {
		return nil, false, err
	}
	_ = write.DeleteCorrectionCheckpoint(lectureID)
	return map[string]any{"path": downloaded, "lectureId": lectureID, "courseId": courseID}, true, nil
}

func videoChoices(pages []videoppt.Page) []videoppt.Choice {
	choices := make([]videoppt.Choice, 0, len(pages))
	for _, page := range pages {
		choices = append(choices, videoppt.Choice{Page: page.Page, Keep: page.Keep, Crop: page.Crop})
	}
	return choices
}

func (inv *Invoker) exportZhiyunFromVideo(ctx context.Context, store contract.StateStore, state *contract.State, // NOSONAR: action contract groups related parameters; changing the signature would break the protocol.
	client *zhiyun.Client, write *storage.WriteDB, input zhiyunExportInput, title, lectureID, courseID, downloaded, pptDirectory string) (any, error) {
	videoURL, _ := client.LessonVideoURL(ctx, input.CourseID, input.SubID)
	if videoURL == "" {
		return nil, contract.InputInvalid("PPT 提取失败，且本课次暂无录像；已下载文件保留")
	}
	inv.videoProgress("智云课件无法读取或全黑，正在改用课程录像；全黑页未裁剪")
	task, err := videoPptService(state).CreateBlankTask(title, nil)
	if err != nil {
		return nil, videoPptError(err)
	}
	if err := client.Download(ctx, videoURL, task.Path); err != nil {
		return nil, contract.Unavailable(err.Error())
	}
	chat, err := inv.videoChat(store, state)
	if err != nil {
		return nil, err
	}
	prepared, err := videoProcessor(state).Prepare(ctx, task, chat, inv.videoProgress)
	if err != nil {
		return nil, videoPptError(err)
	}
	if prepared.Source.Warning != "" {
		return nil, contract.InputInvalid(prepared.Source.Warning + " 视频已保留，可在视频提取页继续检查")
	}
	review, err := videoProcessor(state).Extract(ctx, task, prepared.Source.Crop, defaultVideoPrompt, chat, inv.videoProgress)
	if err != nil {
		return nil, videoPptError(err)
	}
	if review.Warning != "" {
		return nil, contract.InputInvalid(review.Warning + " 视频已保留，可继续检查")
	}
	result, _, err := videoProcessor(state).Export(ctx, task, videoChoices(review.Pages), pptDirectory)
	if err != nil {
		return nil, videoPptError(err)
	}
	if err := inv.persistZhiyunCourseware(write, lectureID, result.Path); err != nil {
		return nil, err
	}
	_ = write.DeleteCorrectionCheckpoint(lectureID)
	if downloaded != "" {
		_ = os.Remove(downloaded)
	}
	if err := videoPptService(state).DiscardCompleted(task.ID); err != nil {
		inv.videoProgress("PDF 已保存，临时文件清理失败：" + err.Error())
	}
	return map[string]any{"path": result.Path, "lectureId": lectureID, "courseId": courseID, "fromVideo": true}, nil
}

func (inv *Invoker) exportZhiyunPPT(ctx context.Context, store contract.StateStore, state *contract.State, // NOSONAR: action contract groups related parameters; changing the signature would break the protocol.
	client *zhiyun.Client, write *storage.WriteDB, input zhiyunExportInput, title, lectureID, courseID, courseName string) (any, error) {
	pptDirectory, err := library.CourseDirectory(state.LibraryRoot, "PPT", courseName)
	if err != nil {
		return nil, err
	}
	resource, resourceErr := client.Resource(ctx, input.CourseID, input.SubID, "ppt")
	downloaded := ""
	if resourceErr == nil {
		downloaded, err = library.ReserveFile(pptDirectory, sanitizeZhiyunFilename(resource.Filename))
		if err != nil {
			return nil, err
		}
		if err := client.Download(ctx, resource.URL, downloaded); err != nil {
			_ = os.Remove(downloaded)
			return nil, contract.Unavailable(err.Error())
		}
		if exported, ok, err := inv.tryZhiyunPPT(write, lectureID, downloaded, courseID); err != nil {
			return nil, err
		} else if ok {
			return exported, nil
		}
	} else if !zhiyunFallbackError(resourceErr) {
		return nil, contract.Unavailable(resourceErr.Error())
	}
	return inv.exportZhiyunFromVideo(ctx, store, state, client, write, input, title, lectureID, courseID, downloaded, pptDirectory)
}

func (inv *Invoker) exportZhiyun(ctx context.Context, store contract.StateStore, state *contract.State, input zhiyunExportInput) (any, error) {
	client, err := inv.zhiyunClient(store)
	if err != nil {
		return nil, err
	}
	client.Progress = func(message string) {
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
	defer write.Close()
	course, err := ensureZhiyunCourse(write, input)
	if err != nil {
		return nil, err
	}
	courseID := storage.ToStringValue(course["id"])
	courseName := storage.ToStringValue(course["name"])
	if err := ensureCourseLibrary(state.LibraryRoot, []string{courseName}); err != nil {
		return nil, err
	}
	title, planned := zhiyunLectureMeta(state, courseID, input, lesson.Title)
	lecture, err := inv.findOrCreateZhiyunLecture(write, courseID, title, planned)
	if err != nil {
		return nil, err
	}
	lectureID := storage.ToStringValue(lecture["id"])
	if input.Kind == "transcript" {
		return inv.exportZhiyunTranscript(ctx, client, write, state, input, lesson, lectureID, courseID, courseName)
	}
	return inv.exportZhiyunPPT(ctx, store, state, client, write, input, title, lectureID, courseID, courseName)
}

func (inv *Invoker) handleZhiyunExport(store contract.StateStore, state *contract.State, params json.RawMessage) (any, error) {
	input, err := parseZhiyunExportInput(params)
	if err != nil {
		return nil, err
	}
	return inv.runJob("zhiyun", func(ctx context.Context) (any, error) {
		return inv.exportZhiyun(ctx, store, state, input)
	})
}
